package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UncertainEffectError 表示外部副作用可能已发生，不能按普通失败继续写入同一目标。
type UncertainEffectError struct{ Reason string }

func (e *UncertainEffectError) Error() string { return e.Reason }

// remoteError 仅包含状态码和提供方错误码，不回显外部响应正文或认证信息。
type remoteError struct {
	Status int
	Code   string
}

func (e *remoteError) Error() string {
	return fmt.Sprintf("外部接口 HTTP %d (%s)", e.Status, e.Code)
}

// externalClient 限时并拒绝重定向，防止向其他域转发凭据。
func externalClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// remoteJSON 有界读取外部 JSON，保留外部整数 ID 精度。
func remoteJSON(ctx context.Context, c *http.Client, method, endpoint string, headers map[string]string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		body = bytes.NewBufferString(encode(payload))
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, body)
	if e != nil {
		return errors.New("外部接口地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := c.Do(req)
	if e != nil {
		return &UncertainEffectError{"外部调用结果未知，需核对原请求"}
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if e != nil {
		return &UncertainEffectError{"外部响应读取失败，结果待核对"}
	}
	if resp.StatusCode >= 500 {
		return &UncertainEffectError{"外部服务错误，副作用结果待核对"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &problem)
		return &remoteError{resp.StatusCode, truncate(problem.Code, 100)}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if e = decoder.Decode(out); e != nil {
		return &UncertainEffectError{"外部 JSON 响应无效，结果待核对"}
	}
	return nil
}

// CodeupClient 使用中心版云效 OpenAPI，API 的 localId 是仓库内稳定 ID。
type CodeupClient struct {
	Token string
	HTTP  *http.Client
	Base  string
}

// NewCodeup 创建不访问网络的适配器。
func NewCodeup(token string) *CodeupClient {
	return &CodeupClient{token, externalClient(), "https://openapi-rdc.aliyuncs.com"}
}

// repoURL 只按服务器配置组成 API 地址，不接受回调传入任意 URL。
func (c *CodeupClient) repoURL(org, repo string) string {
	return c.Base + "/oapi/v1/codeup/organizations/" + url.PathEscape(org) + "/repositories/" + url.PathEscape(repo)
}

// request 校验凭据并通过固定来源请求。
func (c *CodeupClient) request(ctx context.Context, method, path string, in, out any) error {
	if c.Token == "" {
		return errors.New("未配置 Codeup API 凭据")
	}
	return remoteJSON(ctx, c.HTTP, method, path, map[string]string{"x-yunxiao-token": c.Token}, in, out)
}

// Get 读取权威 MR、分支及提交，不采用 Hook 内声称的合并状态。
func (c *CodeupClient) Get(ctx context.Context, org, repo, localID string) (Row, error) {
	out := Row{}
	if _, e := positiveID(localID); e != nil {
		return nil, e
	}
	if e := c.request(ctx, "GET", c.repoURL(org, repo)+"/changeRequests/"+localID, nil, &out); e != nil {
		return nil, e
	}
	if out.S("status") == "MERGED" {
		sha := out.S("mergedRevision")
		if !revisionPattern.MatchString(sha) {
			return nil, errors.New("已合并 MR 缺少有效合并提交")
		}
		commit := Row{}
		if e := c.request(ctx, "GET", c.repoURL(org, repo)+"/commits/"+sha, nil, &commit); e != nil {
			return nil, e
		}
		if commit.S("id") != sha {
			return nil, errors.New("合并提交核实失败")
		}
	}
	var patches []Row
	path := c.repoURL(org, repo) + "/changeRequests/" + localID
	if e := c.request(ctx, "GET", path+"/diffs/patches", nil, &patches); e != nil {
		return nil, e
	}
	version := int64(0)
	head := ""
	for _, patch := range patches {
		if patch.S("relatedMergeItemType") != "MERGE_SOURCE" {
			continue
		}
		if patch.I("versionNo") > version {
			version = patch.I("versionNo")
			head = patch.S("commitId")
		} else if patch.I("versionNo") == version && patch.S("commitId") != head {
			return nil, errors.New("MR 源版本证据冲突")
		}
	}
	if version <= 0 || !revisionPattern.MatchString(head) {
		return nil, errors.New("MR 缺少权威源提交版本")
	}
	// 状态和版本证据必须来自同一稳定快照；期间有修改时等待下一轮核对。
	fresh := Row{}
	if e := c.request(ctx, "GET", path, nil, &fresh); e != nil {
		return nil, e
	}
	for _, key := range []string{"updateTime", "status", "sourceBranch", "targetBranch", "sourceProjectId", "targetProjectId", "mergedRevision"} {
		if out.S(key) != fresh.S(key) {
			return nil, errors.New("MR 核对期间发生变化，等待重新查询")
		}
	}
	out["headRevision"] = head

	return out, nil
}

// Find 在完整分页中按持久标记核对创建结果；查询失败时禁止盲目新建。
func (c *CodeupClient) Find(ctx context.Context, org, repo, marker string) (Row, error) {
	for page := 1; page <= 1000; page++ {
		q := url.Values{"projectIds": {repo}, "search": {marker}, "perPage": {"100"}, "page": {strconv.Itoa(page)}}
		var items []Row
		path := c.Base + "/oapi/v1/codeup/organizations/" + url.PathEscape(org) + "/changeRequests?" + q.Encode()
		if e := c.request(ctx, "GET", path, nil, &items); e != nil {
			return nil, e
		}
		for _, r := range items {
			if strings.Contains(r.S("title"), marker) {
				return c.Get(ctx, org, repo, r.S("localId"))
			}
		}
		if len(items) < 100 {
			return nil, nil
		}
	}
	return nil, errors.New("Codeup 分页超过限制，保留未知结果待核对")
}

// Create 仅创建供人工评审的 MR，平台没有调用自动合并接口。
func (c *CodeupClient) Create(ctx context.Context, org, repo, branch, title, description string) (Row, error) {
	repoID, e := positiveID(repo)
	if e != nil {
		return nil, fail(400, "Codeup 仓库须配置数字 ID")
	}
	out := Row{}
	e = c.request(ctx, "POST", c.repoURL(org, repo)+"/changeRequests", Row{"sourceBranch": branch, "targetBranch": "master", "sourceProjectId": repoID, "targetProjectId": repoID, "title": title, "description": description, "triggerAIReviewRun": false}, &out)
	if e != nil {
		return nil, e
	}
	return c.Get(ctx, org, repo, out.S("localId"))
}
