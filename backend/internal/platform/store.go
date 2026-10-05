package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

// schemaFiles 是版本化的首次部署结构；后续升级增加文件。
//
//go:embed schema/*.sql
var schemaFiles embed.FS

// Row 保存数据库投影；对外 ID 采用十进制字符串，避免浏览器精度损失。
type Row map[string]any

// S 返回字符串或 JSON 的稳定编码。
func (r Row) S(key string) string {
	v := r[key]
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if n, ok := v.(int64); ok {
		return strconv.FormatInt(n, 10)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// I 读取数据库数值或十进制 ID；仅用于已校验的内部投影。
func (r Row) I(key string) int64 {
	switch v := r[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}

// Config 是服务启动配置；所有密钥只来自部署环境，不能经管理 API 读取。
type Config struct {
	DSN               string            // MySQL DSN。
	NodeID            int64             // Snowflake 节点号，单集群不可重复，范围 0..1023。
	SessionKey        string            // 至少 32 字节的会话签名密钥。
	CorpID            string            // 唯一允许的钉钉企业。
	AppID             string            // 企业应用 ID。
	AppSecret         string            // 钉钉应用密钥。
	CoolAppCode       string            // 已开通群能力的酷应用编码。
	BootstrapUnionID  string            // 显式指定的初始管理员已验证 unionId。
	BaseURL           string            // 浏览器可访问的服务地址。
	FrontendDir       string            // 构建后的静态页面目录。
	Migrate           bool              // 首次部署显式建表；正常运行不需要 DDL 权限。
	Demo              bool              // 仅用于隔离的本地演示，主程序限制监听回环地址。
	CodeupToken       string            // Codeup API 凭据，仅宿主执行器使用。
	CodeupHookToken   string            // Codeup Hook 校验密钥。
	ArtifactRoot      string            // 任务产物根目录，独立于复用的工作区。
	RunnerCallbackURL string            // 容器能够访问的事件接收地址。
	Templates         map[string]string // LOG、METRIC、NOTICE、TOPBOX 已发布模板 ID。
}

// Identity 是经过外部身份系统验证的企业身份。
type Identity struct{ CorpID, AppID, UnionID, UserID, OpenID, Name, Avatar string }

// IdentityProvider 只承担外部身份和当前群成员的验证。
type IdentityProvider interface {
	VerifyCode(context.Context, string) (Identity, error)
	Member(context.Context, string, Identity) (bool, error)
}

// App 组合数据库事务与外部适配器，内部业务不依赖浏览器或运行容器。
type App struct {
	lockDB    *sql.DB          // Advisory 锁独立连接，避免占满业务连接后相互等待。
	DB        *sql.DB          // 持久业务库。
	Config    Config           // 不可变部署配置。
	Identity  IdentityProvider // 钉钉身份验证边界。
	Messenger Messenger        // 卡片与吊顶外部边界。
	Codeup    CodeupProvider   // MR 权威查询与创建边界。
	Runtime   Runtime          // 宿主容器边界。
	idMu      sync.Mutex
	lastMS    int64
	sequence  int64
}

// Open 验证部署配置并连接 MySQL，只有显式初始化或隔离演示才执行 DDL。
func Open(ctx context.Context, c Config) (*App, error) {
	if len(c.SessionKey) < 32 || c.CorpID == "" || c.AppID == "" || c.NodeID < 0 || c.NodeID > 1023 {
		return nil, errors.New("会话密钥、企业、应用或节点号配置不完整")
	}
	mc, err := mysql.ParseDSN(c.DSN)
	if err != nil {
		return nil, errors.New("MySQL DSN 格式错误")
	}
	mc.Collation = "utf8mb4_bin"
	mc.ParseTime = true
	mc.MultiStatements = false
	db, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 MySQL：%w", err)
	}
	lockDB, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		db.Close()
		return nil, err
	}
	lockDB.SetMaxIdleConns(8)
	lockDB.SetConnMaxLifetime(5 * time.Minute)
	// 不限制嵌套锁池的已打开连接；HTTP 入口限制总并发，业务池独立限为 16。
	a := &App{DB: db, lockDB: lockDB, Config: c}
	if c.Migrate || c.Demo {
		if err = a.migrate(ctx); err != nil {
			db.Close()
			lockDB.Close()
			return nil, err
		}
	}
	if c.Demo {
		a.Identity = demoIdentity{c}
		a.Messenger = NewDemoMessenger()
		a.Codeup = NewDemoCodeup()
	} else {
		d := NewDingTalk(c)
		a.Identity = d
		a.Messenger = d
		a.Codeup = NewCodeup(c.CodeupToken)
	}
	return a, nil
}

// Close 释放数据库连接。
func (a *App) Close() error {
	if d, ok := a.Identity.(*DingTalk); ok && d.stream != nil {
		d.stream.Close()
	}
	return errors.Join(a.DB.Close(), a.lockDB.Close())
}

// migrate 在应用专用库执行幂等建表；DDL 失败即停止启动。
func (a *App) migrate(ctx context.Context) error {
	b, err := schemaFiles.ReadFile("schema/001_initial.sql")
	if err != nil {
		return err
	}
	for _, s := range strings.Split(string(b), ";") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if _, err = a.DB.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("初始化结构：%w", err)
		}
	}
	return nil
}

// nextID 在节点内生成正数 Snowflake ID；时钟回拨直接失败，不重用序号。
func (a *App) nextID() (int64, error) {
	a.idMu.Lock()
	defer a.idMu.Unlock()
	ms := time.Now().UnixMilli()
	if ms < a.lastMS {
		return 0, errors.New("系统时钟回拨，暂停生成 ID")
	}
	if ms == a.lastMS {
		a.sequence++
		if a.sequence > 4095 {
			return 0, errors.New("单毫秒 ID 容量耗尽，请重试")
		}
	} else {
		a.sequence = 0
	}
	a.lastMS = ms
	return ((ms - 1704067200000) << 22) | (a.Config.NodeID << 12) | a.sequence, nil
}

// digest 为幂等和内容一致性生成 SHA256。
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// encode 将已约束的业务数据编码为 JSON。
func encode(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// nonce 生成不包含业务信息的随机请求标识。
func nonce() string { return rand.Text() }

// querier 是数据库与事务共享的最小查询边界。
type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// rows 查询有界投影并保持 JSON、整数和 ID 的类型。
func rows(ctx context.Context, q querier, query string, args ...any) ([]Row, error) {
	rs, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	names, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	types, err := rs.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := []Row{}
	for rs.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := Row{}
		for i, k := range names {
			v := vals[i]
			if bs, ok := v.([]byte); ok {
				switch types[i].DatabaseTypeName() {
				case "JSON":
					var j any
					decoder := json.NewDecoder(bytes.NewReader(bs))
					decoder.UseNumber()
					if err := decoder.Decode(&j); err != nil {
						return nil, err
					}
					v = j
				default:
					v = string(bs)
				}
			}
			if k == "id" || strings.HasSuffix(k, "_id") && types[i].DatabaseTypeName() == "BIGINT" {
				switch n := v.(type) {
				case int64:
					v = strconv.FormatInt(n, 10)
				case uint64:
					v = strconv.FormatUint(n, 10)
				}
			}
			r[k] = v
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// one 查询可缺省的一行，不把缺失伪装为错误行。
func one(ctx context.Context, q querier, query string, args ...any) (Row, error) {
	r, e := rows(ctx, q, query, args...)
	if e != nil {
		return nil, e
	}
	if len(r) == 0 {
		return nil, nil
	}
	return r[0], nil
}

// insert 仅接受应用内部白名单构造的表名和字段，参数全部绑定。
func (a *App) insert(ctx context.Context, q querier, table string, r Row) (int64, error) {
	id, err := a.nextID()
	if err != nil {
		return 0, err
	}
	r["id"] = id
	r["create_time"] = time.Now().UnixMilli()
	r["update_time"] = r["create_time"]
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]any, len(keys))
	marks := make([]string, len(keys))
	for i, k := range keys {
		args[i] = r[k]
		marks[i] = "?"
	}
	_, err = q.ExecContext(ctx, "INSERT INTO "+table+" (`"+strings.Join(keys, "`,`")+"`) VALUES ("+strings.Join(marks, ",")+")", args...)
	return id, err
}

// update 仅更新显式列，避免覆盖同一实体的其他并发业务字段。
func update(ctx context.Context, q querier, table string, id int64, r Row) error {
	r["update_time"] = time.Now().UnixMilli()
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := []any{}
	sets := []string{}
	for _, k := range keys {
		sets = append(sets, "`"+k+"`=?")
		args = append(args, r[k])
	}
	args = append(args, id)
	_, err := q.ExecContext(ctx, "UPDATE "+table+" SET "+strings.Join(sets, ",")+" WHERE id=?", args...)
	return err
}

// transaction 对短事务中的数据库死锁执行有限重试；外部副作用必须在事务外执行。
func (a *App) transaction(ctx context.Context, f func(*sql.Tx) error) error {
	for attempt := 0; attempt < 3; attempt++ {
		tx, e := a.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if e != nil {
			return e
		}
		e = f(tx)
		if e == nil {
			e = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		var me *mysql.MySQLError
		if !errors.As(e, &me) || (me.Number != 1213 && me.Number != 1205) {
			return e
		}
	}
	return errors.New("事务竞争，请重试")
}

// mustExec 统一传播事务内写入错误。
func mustExec(ctx context.Context, q querier, query string, args ...any) error {
	_, err := q.ExecContext(ctx, query, args...)
	return err
}

// decodeJSON 解码数据库 JSON 投影到经过类型约束的协议。
func decodeJSON(v any, out any) error { return json.Unmarshal([]byte(encode(v)), out) }
