package platform_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mylxy/ai-alertops/backend/internal/platform"
)

// TestDingTalkTransport 校验官方身份与实时成员协议，以及卡片更新和吊顶关闭的请求形状。
func TestDingTalkTransport(t *testing.T) {
	// 准备：仅替代远程 HTTP 服务，不替代被测适配器。
	calls := map[string]int{}
	memberPage := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" "+r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		var input map[string]any
		if r.Method != "GET" {
			if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
				t.Error(e)
			}
		}
		var result any
		switch r.URL.Path {
		case "/v1.0/oauth2/userAccessToken":
			if input["code"] != "verified-code" || input["grantType"] != "authorization_code" {
				t.Error(input)
			}
			result = map[string]any{"accessToken": "user-token", "corpId": "corp"}
		case "/v1.0/oauth2/accessToken":
			result = map[string]any{"accessToken": "app-token", "expireIn": 7200}
		case "/v1.0/contact/users/me":
			if r.Header.Get("x-acs-dingtalk-access-token") != "user-token" {
				t.Error("身份请求凭据错误")
			}
			result = map[string]any{"unionId": "union", "openId": "open", "nick": "用户"}
		case "/topapi/user/getbyunionid":
			if input["unionid"] != "union" || r.URL.Query().Get("access_token") != "app-token" {
				t.Error(input)
			}
			result = map[string]any{"errcode": 0, "result": map[string]any{"userid": "userid"}}
		case "/v1.0/im/sceneGroups/members/batchQuery":
			memberPage++
			if input["openConversationId"] != "group" || input["coolAppCode"] != "cool" || input["maxResults"] != float64(100) {
				t.Error(input)
			}
			if memberPage == 1 {
				result = map[string]any{"success": true, "memberUserIds": []string{"someone"}, "hasMore": true, "nextToken": "p2"}
			} else {
				if input["nextToken"] != "p2" {
					t.Error(input)
				}
				result = map[string]any{"success": true, "memberUserIds": []string{"userid"}, "hasMore": false}
			}
		case "/v1.0/card/instances/createAndDeliver":
			if input["outTrackId"] != "stable" || input["callbackType"] != "STREAM" || input["openSpaceId"] != "dtv1.card//IM_GROUP.group" {
				t.Error(input)
			}
			result = map[string]any{"success": true, "result": map[string]any{"deliverResults": []any{map[string]any{"success": true}}}}
		case "/v1.0/card/instances":
			if r.Method != "PUT" || input["outTrackId"] != "stable" {
				t.Error(input)
			}
			result = map[string]any{"success": true}
		case "/v2.0/im/topBoxes/close":
			if input["openConversationId"] != "group" || input["conversationType"] != float64(1) {
				t.Error(input)
			}
			result = map[string]any{"success": true}
		default:
			t.Error("未预期的外部接口", r.URL.Path)
			w.WriteHeader(404)
			result = map[string]string{"error": "unexpected"}
		}
		if e := json.NewEncoder(w).Encode(result); e != nil {
			t.Error(e)
		}
	}))
	defer remote.Close()
	d := platform.NewDingTalk(platform.Config{CorpID: "corp", AppID: "app", AppSecret: "secret", CoolAppCode: "cool"})
	d.APIBase = remote.URL
	d.OAPIBase = remote.URL
	// 执行：企业身份验证、成员分页查询、首发/更新、真正关闭吊顶。
	identity, e := d.VerifyCode(context.Background(), "verified-code")
	if e != nil {
		t.Fatal(e)
	}
	yes, e := d.Member(context.Background(), "group", identity)
	if e != nil || !yes {
		t.Fatalf("成员关系 %t %v", yes, e)
	}
	card := platform.Row{"template_id": "template", "out_track_id": "stable", "conversation_id": "group", "sent_version": 0, "h5_url": "https://alerts.example/h5/groups/1?round=2"}
	if e = d.Card(context.Background(), card); e != nil {
		t.Fatal(e)
	}
	card["sent_version"] = 1
	if e = d.Card(context.Background(), card); e != nil {
		t.Fatal(e)
	}
	card["desired_status"] = "CLOSED"
	if e = d.Topbox(context.Background(), card); e != nil {
		t.Fatal(e)
	}
	// 验证：同一外部身份只创建一次，后续使用更新而非再次发消息。
	if calls["POST /v1.0/card/instances/createAndDeliver"] != 1 || calls["PUT /v1.0/card/instances"] != 1 || calls["POST /v2.0/im/topBoxes/close"] != 1 {
		t.Fatal(calls)
	}
}

// TestCardPresentation 校验真正使用的部署参数：动态 MR、凭据脱敏和双端内嵌 URL。
func TestCardPresentation(t *testing.T) {
	// 准备：四个合并请求与包含凭据的上游日志。
	mrs := []platform.Row{}
	for _, id := range []string{"1", "2", "3", "4"} {
		mrs = append(mrs, platform.Row{"display_number": id, "url": "https://codeup.aliyun.com/org/repo/change/" + id, "merge_status": "OPEN"})
	}
	rawURL := "https://alerts.example/h5/groups/1?round=2"
	input := platform.Row{"card_type": "LOG", "round_status": "PROCESSING", "h5_url": rawURL, "out_track_id": "card", "version": 2, "detail": platform.Row{"round": platform.Row{"alert_type": "LOG", "round_status": "PROCESSING", "alert_no": "AL123", "title": "异常", "first_event_time": 1, "last_event_time": 2, "event_count": 4}, "evidence": platform.Row{"message": `{"apiKey":"must-not-leak"} Bearer sensitive-token`}, "records": []platform.Row{}, "merge_requests": mrs}}
	// 执行：通过实际渲染边界得到模板参数。
	p := platform.CardParams(input)
	// 验证：没有固定两条的上限；字段与 URL 编码可逐层还原。
	if strings.Count(p["mr_markdown"], "合并请求 #") != 4 || p["phase"] != "orange" {
		t.Fatal(p)
	}
	if strings.Contains(p["evidence"], "must-not-leak") || strings.Contains(p["body"], "sensitive-token") {
		t.Fatal("卡片泄露凭据")
	}
	link, e := url.Parse(p["h5_url"])
	if e != nil {
		t.Fatal(e)
	}
	pc, e := url.Parse(link.Query().Get("pcLink"))
	if e != nil {
		t.Fatal(e)
	}
	mobile, e := url.Parse(link.Query().Get("mobileLink"))
	if e != nil {
		t.Fatal(e)
	}
	if pc.Query().Get("pc_slide") != "true" || pc.Query().Get("url") != rawURL || mobile.Query().Get("pageUrl") != rawURL || mobile.Query().Get("panelHeight") != "percent83" {
		t.Fatal("钉钉内嵌链接编码错误")
	}
}

// TestCodeupMergeEvidence 使用真实 HTTP 适配器核实 MR 固定版本，不依赖已删除的源分支。
func TestCodeupMergeEvidence(t *testing.T) {
	// 准备：权威 MR 已合并，固定版本给出源提交；第二次查询模拟并发修改。
	unstable := false
	reads := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-yunxiao-token") != "test-token" {
			t.Error("未携带提供方凭据")
		}
		var result any
		switch {
		case strings.HasSuffix(r.URL.Path, "/diffs/patches"):
			result = []map[string]any{{"versionNo": 1, "relatedMergeItemType": "MERGE_SOURCE", "commitId": strings.Repeat("b", 40)}, {"versionNo": 2, "relatedMergeItemType": "MERGE_SOURCE", "commitId": strings.Repeat("a", 40)}}
		case strings.Contains(r.URL.Path, "/commits/"):
			result = map[string]any{"id": strings.Repeat("c", 40)}
		default:
			reads++
			updated := "2026-10-05T01:00:00Z"
			if unstable && reads%2 == 0 {
				updated = "2026-10-05T01:01:00Z"
			}
			result = map[string]any{"localId": 1, "status": "MERGED", "mergedRevision": strings.Repeat("c", 40), "sourceBranch": "hotfix", "targetBranch": "master", "sourceProjectId": 1, "targetProjectId": 1, "updateTime": updated}
		}
		if e := json.NewEncoder(w).Encode(result); e != nil {
			t.Error(e)
		}
	}))
	defer remote.Close()
	provider := platform.NewCodeup("test-token")
	provider.Base = remote.URL
	// 执行：先读取稳定证据，再读取核对途中发生变化的 MR。
	got, e := provider.Get(context.Background(), "org", "1", "1")
	if e != nil {
		t.Fatal(e)
	}
	// 验证：真实源提交来自最高版本；变更中的状态不被拼接为合并证明。
	if got.S("headRevision") != strings.Repeat("a", 40) {
		t.Fatal("源提交未来自最高版本")
	}
	unstable = true
	if _, e = provider.Get(context.Background(), "org", "1", "1"); e == nil {
		t.Fatal("并发修改未阻止不一致的合并证明")
	}
}

// TestDingTalkBusinessFailure 防止 HTTP 成功掩盖业务失败或缺少投递确认。
func TestDingTalkBusinessFailure(t *testing.T) {
	// 准备：真实适配器接入受控响应，逐一覆盖业务失败、空响应和部分投递。
	for _, sample := range []struct {
		name, body string
		create     bool
	}{
		{"更新业务失败", `{"success":false}`, false},
		{"更新结果失败", `{"success":true,"result":false}`, false},
		{"更新缺少确认", `{}`, false},
		{"创建未投递", `{"success":true,"result":{"deliverResults":[{"success":false}]}}`, true},
		{"创建缺少投递结果", `{"success":true}`, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1.0/oauth2/accessToken" {
					_, _ = w.Write([]byte(`{"accessToken":"token","expireIn":7200}`))
					return
				}
				_, _ = w.Write([]byte(sample.body))
			}))
			defer remote.Close()
			d := platform.NewDingTalk(platform.Config{AppSecret: "secret", CoolAppCode: "cool"})
			d.APIBase = remote.URL
			r := platform.Row{"template_id": "template", "sent_version": 1, "desired_status": "OPEN", "observed_status": "OPEN"}
			if sample.create {
				r["sent_version"] = 0
			}
			// 执行和验证：普通卡与已有吊顶均不得把未确认的响应视为成功。
			if e := d.Card(context.Background(), r); e == nil {
				t.Fatal("错误响应被标记为卡片已同步")
			}
			if !sample.create {
				if e := d.Topbox(context.Background(), r); e == nil {
					t.Fatal("错误响应被标记为吊顶已同步")
				}
			}
		})
	}
}
