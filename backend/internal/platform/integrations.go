package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Messenger 以稳定 outTrackId 同步外部卡片和吊顶。
type Messenger interface {
	Card(context.Context, Row) error
	Topbox(context.Context, Row) error
}

// CodeupProvider 查询外部权威合并状态。
type CodeupProvider interface {
	Get(context.Context, string, string, string) (Row, error)
	Find(context.Context, string, string, string) (Row, error)
	Create(context.Context, string, string, string, string, string) (Row, error)
}

// Runtime 在宿主机管理 Docker，不向容器暴露 socket。
type Runtime interface {
	Inspect(context.Context, string) (Row, error)
	Start(context.Context, Row) error
}

// DemoMessenger 是用于完整本地链路的外部消息服务替身。
type DemoMessenger struct {
	mu       sync.Mutex
	Cards    map[string]Row
	Topboxes map[string]Row
	Failure  error
	Calls    int
}

// NewDemoMessenger 创建独立的消息服务。
func NewDemoMessenger() *DemoMessenger {
	return &DemoMessenger{Cards: map[string]Row{}, Topboxes: map[string]Row{}}
}

// Card 模拟外部稳定 ID，重试不会重复创建消息。
func (d *DemoMessenger) Card(_ context.Context, r Row) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Calls++
	if d.Failure != nil {
		return d.Failure
	}
	d.Cards[r.S("out_track_id")] = r
	return nil
}

// Topbox 模拟真正打开与关闭吊顶。
func (d *DemoMessenger) Topbox(_ context.Context, r Row) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.Failure != nil {
		return d.Failure
	}
	d.Topboxes[r.S("conversation_id")] = r
	return nil
}

// Snapshot 返回外部投递事实，供测试界面查看。
func (d *DemoMessenger) Snapshot() Row {
	d.mu.Lock()
	defer d.mu.Unlock()
	cards := []Row{}
	for _, r := range d.Cards {
		cards = append(cards, r)
	}
	tops := []Row{}
	for _, r := range d.Topboxes {
		tops = append(tops, r)
	}
	return Row{"cards": cards, "topboxes": tops, "calls": d.Calls}
}

// DemoCodeup 保存明确设置的外部 MR 权威状态。
type DemoCodeup struct {
	mu    sync.Mutex
	Items map[string]Row
}

// NewDemoCodeup 创建隔离的外部仓库替身。
func NewDemoCodeup() *DemoCodeup { return &DemoCodeup{Items: map[string]Row{}} }

// Get 缺失对象明确报错，不把 Hook 报文当作权威状态。
func (d *DemoCodeup) Get(_ context.Context, org, repo, id string) (Row, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.Items[org+":"+repo+":"+id]
	if r == nil {
		return nil, errors.New("外部 MR 尚未登记")
	}
	return r, nil
}

// Find 按稳定标识核对模拟服务内已存在的请求。
func (d *DemoCodeup) Find(_ context.Context, org, repo, marker string) (Row, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, r := range d.Items {
		if strings.HasPrefix(key, org+":"+repo+":") && strings.Contains(r.S("title"), marker) {
			return r, nil
		}
	}
	return nil, nil
}

// Create 模拟 Codeup 创建操作，不访问远程仓库。
func (d *DemoCodeup) Create(_ context.Context, org, repo, branch, title, description string) (Row, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := fmt.Sprint(len(d.Items) + 1)
	r := Row{"localId": id, "projectId": repo, "sourceProjectId": repo, "targetProjectId": repo, "sourceBranch": branch, "targetBranch": "master", "status": "UNDER_REVIEW", "title": title, "description": description, "webUrl": "https://codeup.aliyun.com/" + org + "/" + repo + "/change/" + id, "headRevision": strings.Repeat("a", 40), "updateTime": time.Now().UTC().Format(time.RFC3339Nano)}
	d.Items[org+":"+repo+":"+id] = r
	return r, nil
}

// Set 替换模拟外部状态，调用后需通过真实 Hook 驱动平台同步。
func (d *DemoCodeup) Set(org, repo, id string, r Row) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Items[org+":"+repo+":"+id] = r
}

// PendingResolver 只有能证明之前调用已完成的外部适配器才实现此接口。
// 钉钉当前适配器没有可靠查询/版本条件能力，未知结果保持暂停，不能假装已协调。
type PendingResolver interface {
	ResolvePending(context.Context, string, string) error
}

// ResolvePending 模拟服务的调用与存储在同一互斥区完成，没有后台未决请求。
func (d *DemoMessenger) ResolvePending(_ context.Context, kind, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return nil
}
