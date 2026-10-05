//go:build integration

package platform_test

import (
	"context"
	"encoding/json"
	"github.com/mylxy/ai-alertops/backend/internal/platform"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/card"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCardIdentityAndIndependentNotes 验证原卡连续独立备注、请求重试、退群和停用的当前权限。
func TestCardIdentityAndIndependentNotes(t *testing.T) {
	// 准备：已有普通用户和一张告警卡，远程 HTTP 只提供经过验证的同企业身份。
	app, server := testPlatform(t)
	ctx := context.Background()
	_, _, adminCookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "admin"}, nil, nil)
	_, member, memberCookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	if member["role"] != "USER" {
		t.Fatal("首次登录不是普通用户")
	}
	if e := app.DemoScenario(ctx, "log"); e != nil {
		t.Fatal(e)
	}
	if e := app.ProcessEvents(ctx, 100); e != nil {
		t.Fatal(e)
	}
	_, list, _ := requestJSON(t, server, "GET", "/api/rounds", nil, memberCookies[0], nil)
	id := list["items"].([]any)[0].(map[string]any)["id"].(string)
	_, detail, _ := requestJSON(t, server, "GET", "/api/rounds/"+id, nil, memberCookies[0], nil)
	track := detail["cards"].([]any)[0].(map[string]any)["out_track_id"].(string)
	inGroup := true
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			body = map[string]any{"accessToken": "test-token", "expireIn": 7200}
		case "/topapi/v2/user/get":
			body = map[string]any{"errcode": 0, "result": map[string]any{"userid": "member", "unionid": "member", "name": "普通用户"}}
		case "/v1.0/im/sceneGroups/members/batchQuery":
			members := []string{}
			if inGroup {
				members = append(members, "member")
			}
			body = map[string]any{"success": true, "memberUserIds": members, "hasMore": false}
		default:
			t.Error("未知身份查询", r.URL.Path)
			w.WriteHeader(404)
			body = map[string]any{}
		}
		if e := json.NewEncoder(w).Encode(body); e != nil {
			t.Error(e)
		}
	}))
	defer remote.Close()
	ding := platform.NewDingTalk(platform.Config{CorpID: app.Config.CorpID, AppID: app.Config.AppID, AppSecret: "test-secret"})
	ding.APIBase = remote.URL
	ding.OAPIBase = remote.URL
	app.Identity = ding
	req := &card.CardRequest{CorpId: app.Config.CorpID, UserId: "member", UserIdType: 1, SpaceId: "demo-group", OutTrackId: track, CardActionData: card.PrivateCardActionData{CardPrivateData: card.CardPrivateData{Params: map[string]any{"action": "note", "operation_key": "initial", "content": "相同文字的独立提交"}}}}
	// 执行：同一次回调重投返回同一个下一操作键，新的提交键允许相同文字再次追加。
	first, e := app.HandleCardCallback(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	retry, e := app.HandleCardCallback(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	next := first.UserPrivateData.CardParamMap["operation_key"]
	if next == "" || next == "initial" || next != retry.UserPrivateData.CardParamMap["operation_key"] {
		t.Fatal("原卡操作键未稳定轮换")
	}
	req.CardActionData.CardPrivateData.Params["operation_key"] = next
	if _, e = app.HandleCardCallback(ctx, req); e != nil {
		t.Fatal(e)
	}
	// 验证：两条记录但仍一张群卡；退群后拒绝旧卡，停用后旧会话立即失效。
	_, detail, _ = requestJSON(t, server, "GET", "/api/rounds/"+id, nil, memberCookies[0], nil)
	if len(detail["records"].([]any)) != 2 || len(detail["cards"].([]any)) != 1 {
		t.Fatal("原卡提交错误增发或去重")
	}
	inGroup = false
	req.CardActionData.CardPrivateData.Params["operation_key"] = "after-leave"
	if _, e = app.HandleCardCallback(ctx, req); e == nil {
		t.Fatal("退群后仍能操作旧卡")
	}
	status, body, _ := requestJSON(t, server, "PUT", "/api/admin/config/sys_user", map[string]any{"id": member["id"], "role": "USER", "account_status": "DISABLED"}, adminCookies[0], nil)
	if status != 200 {
		t.Fatal(body)
	}
	status, _, _ = requestJSON(t, server, "GET", "/api/rounds", nil, memberCookies[0], nil)
	if status != 403 {
		t.Fatal("停用后旧会话未失效")
	}
	inGroup = true
	if _, e = app.HandleCardCallback(ctx, req); e == nil {
		t.Fatal("卡片登录恢复了停用账号")
	}
}
