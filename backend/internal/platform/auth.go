package platform

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// problemError 将业务失败与不可公开的数据库错误区分。
type problemError struct {
	Status  int
	Message string
}

func (e *problemError) Error() string { return e.Message }

// fail 构造可公开的业务错误。
func fail(status int, message string) error { return &problemError{status, message} }

// login 只将可信企业身份映射到账号；重新登录不提升权限、不恢复停用账号。
func (a *App) login(ctx context.Context, identity Identity) (Row, error) {
	if identity.CorpID != a.Config.CorpID || identity.AppID != a.Config.AppID || identity.UnionID == "" || len(identity.UnionID) > 255 {
		return nil, fail(403, "钉钉身份不属于本企业应用")
	}
	var user Row
	err := a.withExternalLock(ctx, "login:"+digest(identity.CorpID+":"+identity.AppID+":"+identity.UnionID), func() error {
		return a.transaction(ctx, func(tx *sql.Tx) error {
			// 同一个企业身份的新建通过唯一键竞争；重复登录按已存在身份重试。
			old, e := one(ctx, tx, "SELECT * FROM sys_dingtalk_identity WHERE corp_id=? AND app_id=? AND identity_type='UNION_ID' AND identity_value=? FOR UPDATE", identity.CorpID, identity.AppID, identity.UnionID)
			if e != nil {
				return e
			}
			var uid int64
			if old == nil {
				role := "USER"
				if a.Config.BootstrapUnionID != "" && identity.UnionID == a.Config.BootstrapUnionID {
					role = "ADMIN"
				}
				uid, e = a.insert(ctx, tx, "sys_user", Row{"display_name": identity.Name, "avatar_url": identity.Avatar, "role": role, "account_status": "ACTIVE", "last_login_time": time.Now().UnixMilli()})
				if e != nil {
					return e
				}
			} else {
				uid = old.I("sys_user_id")
			}
			for kind, value := range map[string]string{"UNION_ID": identity.UnionID, "USER_ID": identity.UserID, "OPEN_ID": identity.OpenID} {
				if value == "" {
					continue
				}
				r, e := one(ctx, tx, "SELECT * FROM sys_dingtalk_identity WHERE corp_id=? AND app_id=? AND identity_type=? AND identity_value=? FOR UPDATE", identity.CorpID, identity.AppID, kind, value)
				if e != nil {
					return e
				}
				if r != nil {
					if r.I("sys_user_id") != uid {
						return fail(409, "已验证身份关联冲突")
					}
					continue
				}
				_, e = a.insert(ctx, tx, "sys_dingtalk_identity", Row{"sys_user_id": uid, "corp_id": identity.CorpID, "app_id": identity.AppID, "identity_type": kind, "identity_value": value, "verified_time": time.Now().UnixMilli()})
				if e != nil {
					return e
				}
			}
			user, e = one(ctx, tx, "SELECT * FROM sys_user WHERE id=? FOR UPDATE", uid)
			if e != nil {
				return e
			}
			if user == nil || user.S("account_status") != "ACTIVE" {
				return fail(403, "账号已停用")
			}
			return update(ctx, tx, "sys_user", uid, Row{"display_name": identity.Name, "avatar_url": identity.Avatar, "last_login_time": time.Now().UnixMilli()})
		})
	})
	return user, err
}

// signature 用部署密钥绑定会话，不在浏览器中存储钉钉 token。
func (a *App) signature(s string) string {
	h := hmac.New(sha256.New, []byte(a.Config.SessionKey))
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// sessionCookie 创建短期 HttpOnly 会话。
func (a *App) sessionCookie(uid int64) *http.Cookie {
	s := strconv.FormatInt(uid, 10) + "." + strconv.FormatInt(time.Now().Add(8*time.Hour).Unix(), 10)
	return &http.Cookie{Name: "alertops_session", Value: s + "." + a.signature(s), Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.Config.BaseURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: 8 * 3600}
}

// currentUser 每次重新读取账号权限，使停用与角色调整即时生效。
func (a *App) currentUser(r *http.Request) (Row, error) {
	c, err := r.Cookie("alertops_session")
	if err != nil {
		return nil, fail(401, "请先完成钉钉登录")
	}
	p := strings.Split(c.Value, ".")
	if len(p) != 3 || !hmac.Equal([]byte(p[2]), []byte(a.signature(strings.Join(p[:2], ".")))) {
		return nil, fail(401, "登录已失效")
	}
	expiry, e := strconv.ParseInt(p[1], 10, 64)
	if e != nil || expiry <= time.Now().Unix() {
		return nil, fail(401, "登录已过期")
	}
	uid, e := strconv.ParseInt(p[0], 10, 64)
	if e != nil {
		return nil, fail(401, "登录无效")
	}
	u, e := one(r.Context(), a.DB, "SELECT * FROM sys_user WHERE id=?", uid)
	if e != nil {
		return nil, e
	}
	if u == nil || u.S("account_status") != "ACTIVE" {
		return nil, fail(403, "账号已停用")
	}
	return u, nil
}

// verifyMember 每次写操作向身份系统确认成员；数据库缓存只留审计快照。
func (a *App) verifyMember(ctx context.Context, uid, gid int64) error {
	group, err := one(ctx, a.DB, "SELECT * FROM alert_group WHERE id=?", gid)
	if err != nil {
		return err
	}
	if group == nil || group.S("corp_id") != a.Config.CorpID || group.S("app_id") != a.Config.AppID {
		return fail(403, "群不属于本企业应用")
	}
	ids, err := rows(ctx, a.DB, "SELECT * FROM sys_dingtalk_identity WHERE sys_user_id=? AND corp_id=? AND app_id=?", uid, a.Config.CorpID, a.Config.AppID)
	if err != nil {
		return err
	}
	identity := Identity{CorpID: a.Config.CorpID, AppID: a.Config.AppID}
	for _, v := range ids {
		switch v.S("identity_type") {
		case "USER_ID":
			identity.UserID = v.S("identity_value")
		case "UNION_ID":
			identity.UnionID = v.S("identity_value")
		case "OPEN_ID":
			identity.OpenID = v.S("identity_value")
		}
	}
	yes, err := a.Identity.Member(ctx, group.S("conversation_id"), identity)
	if err != nil {
		return fail(503, "暂时无法确认群成员身份，请稍后重试")
	}
	state := "LEFT"
	if yes {
		state = "MEMBER"
	}
	now := time.Now().UnixMilli()
	id, e := a.nextID()
	if e != nil {
		return e
	}
	if err = mustExec(ctx, a.DB, "INSERT INTO alert_group_member (id,create_time,update_time,alert_group_id,sys_user_id,member_status,verified_time,expire_time,verification_source) VALUES (?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE member_status=VALUES(member_status),verified_time=VALUES(verified_time),expire_time=VALUES(expire_time),update_time=VALUES(update_time)", id, now, now, gid, uid, state, now, now, "DINGTALK"); err != nil {
		return err
	}
	if !yes {
		return fail(403, "只有当前告警群成员可以处理")
	}
	return nil
}

// secretEqual 避免通过字符串比较泄露鉴权信息。
func secretEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// demoIdentity 只用于本地独立演示；不接受任意客户端身份字符串。
type demoIdentity struct{ config Config }

func (d demoIdentity) VerifyCode(_ context.Context, code string) (Identity, error) {
	if code != "admin" && code != "member" && code != "outsider" {
		return Identity{}, errors.New("未知演示身份")
	}
	return Identity{CorpID: d.config.CorpID, AppID: d.config.AppID, UnionID: code, UserID: code, Name: map[string]string{"admin": "演示管理员", "member": "群内普通用户", "outsider": "非群成员用户"}[code]}, nil
}
func (d demoIdentity) Member(_ context.Context, group string, i Identity) (bool, error) {
	return group == "demo-group" && (i.UnionID == "admin" || i.UnionID == "member"), nil
}
