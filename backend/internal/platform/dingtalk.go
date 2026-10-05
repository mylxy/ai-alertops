package platform

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/card"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/logger"
)

// DingTalk 调用官方 OAuth、通讯录、卡片与吊顶 API，回调通过官方 Stream 验证连接。
type DingTalk struct {
	Config   Config
	HTTP     *http.Client
	APIBase  string
	OAPIBase string
	mu       sync.Mutex
	token    string
	expiry   time.Time
	stream   *client.StreamClient
}

// NewDingTalk 构造适配器，构造时不访问网络。
func NewDingTalk(c Config) *DingTalk {
	return &DingTalk{Config: c, HTTP: externalClient(), APIBase: "https://api.dingtalk.com", OAPIBase: "https://oapi.dingtalk.com"}
}

// appToken 在过期前刷新应用 token，不向日志或管理页面暴露凭据。
func (d *DingTalk) appToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token != "" && time.Now().Before(d.expiry) {
		return d.token, nil
	}
	if d.Config.AppSecret == "" {
		return "", errors.New("未配置钉钉应用密钥")
	}
	var out struct {
		Token  string `json:"accessToken"`
		Expire int64  `json:"expireIn"`
	}
	if e := remoteJSON(ctx, d.HTTP, "POST", d.APIBase+"/v1.0/oauth2/accessToken", nil, Row{"appKey": d.Config.AppID, "appSecret": d.Config.AppSecret}, &out); e != nil {
		return "", e
	}
	if out.Token == "" || out.Expire <= 0 {
		return "", errors.New("钉钉 token 响应不完整")
	}
	d.token = out.Token
	d.expiry = time.Now().Add(time.Duration(out.Expire)*time.Second - time.Minute)
	return d.token, nil
}

// request 为新 API 添加应用凭据。
func (d *DingTalk) request(ctx context.Context, method, path string, in, out any) error {
	token, e := d.appToken(ctx)
	if e != nil {
		return e
	}
	return remoteJSON(ctx, d.HTTP, method, d.APIBase+path, map[string]string{"x-acs-dingtalk-access-token": token}, in, out)
}

// legacy 为官方旧通讯录 API 添加应用凭据，并校验业务 errcode。
func (d *DingTalk) legacy(ctx context.Context, path string, in any) (Row, error) {
	token, e := d.appToken(ctx)
	if e != nil {
		return nil, e
	}
	out := Row{}
	if e = remoteJSON(ctx, d.HTTP, "POST", d.OAPIBase+path+"?access_token="+url.QueryEscape(token), nil, in, &out); e != nil {
		return nil, e
	}
	if out.I("errcode") != 0 {
		return nil, errors.New("钉钉通讯录验证失败")
	}
	result, ok := out["result"].(map[string]any)
	if !ok {
		return nil, errors.New("钉钉通讯录响应缺少结果")
	}
	return Row(result), nil
}

// VerifyCode 通过官方授权码确认企业与 unionId，再确认企业 userid 关联。
func (d *DingTalk) VerifyCode(ctx context.Context, code string) (Identity, error) {
	var token struct {
		Access string `json:"accessToken"`
		Corp   string `json:"corpId"`
	}
	if code == "" {
		return Identity{}, errors.New("缺少授权码")
	}
	if e := remoteJSON(ctx, d.HTTP, "POST", d.APIBase+"/v1.0/oauth2/userAccessToken", nil, Row{"clientId": d.Config.AppID, "clientSecret": d.Config.AppSecret, "code": code, "grantType": "authorization_code"}, &token); e != nil {
		return Identity{}, e
	}
	if token.Access == "" || token.Corp != d.Config.CorpID {
		return Identity{}, errors.New("授权企业不匹配")
	}
	var me struct {
		Union  string `json:"unionId"`
		Open   string `json:"openId"`
		Nick   string `json:"nick"`
		Avatar string `json:"avatarUrl"`
	}
	if e := remoteJSON(ctx, d.HTTP, "GET", d.APIBase+"/v1.0/contact/users/me", map[string]string{"x-acs-dingtalk-access-token": token.Access}, nil, &me); e != nil {
		return Identity{}, e
	}
	if me.Union == "" {
		return Identity{}, errors.New("钉钉身份缺少 unionId")
	}
	mapping, e := d.legacy(ctx, "/topapi/user/getbyunionid", Row{"unionid": me.Union})
	if e != nil {
		return Identity{}, e
	}
	if mapping.S("userid") == "" {
		return Identity{}, errors.New("无法验证企业用户")
	}
	return Identity{CorpID: token.Corp, AppID: d.Config.AppID, UnionID: me.Union, OpenID: me.Open, UserID: mapping.S("userid"), Name: me.Nick, Avatar: me.Avatar}, nil
}

// Member 分页实时检查场景群成员，任何失败都不授权写入。
func (d *DingTalk) Member(ctx context.Context, group string, i Identity) (bool, error) {
	if i.CorpID != d.Config.CorpID || i.UserID == "" {
		return false, errors.New("缺少已验证的企业 userid")
	}
	cursor := ""
	for range 1000 {
		var out struct {
			Success bool     `json:"success"`
			Members []string `json:"memberUserIds"`
			More    bool     `json:"hasMore"`
			Next    string   `json:"nextToken"`
		}
		in := Row{"openConversationId": group, "coolAppCode": d.Config.CoolAppCode, "nextToken": cursor, "maxResults": 100}
		if e := d.request(ctx, "POST", "/v1.0/im/sceneGroups/members/batchQuery", in, &out); e != nil {
			return false, e
		}
		if !out.Success {
			return false, errors.New("钉钉未确认成员列表")
		}
		for _, member := range out.Members {
			if member == i.UserID {
				return true, nil
			}
		}
		if !out.More {
			return false, nil
		}
		if out.Next == "" || out.Next == cursor {
			return false, errors.New("钉钉成员分页无效")
		}
		cursor = out.Next
	}
	return false, errors.New("群成员分页超过限制")
}

// Card 更新已经确认的实例；首次和未知投递始终复用同一个 outTrackId。
func (d *DingTalk) Card(ctx context.Context, r Row) error {
	if r.S("template_id") == "" {
		return errors.New("尚未配置已发布卡片模板")
	}
	params := CardParams(r)
	payload := Row{"outTrackId": r.S("out_track_id"), "userIdType": 1, "cardData": Row{"cardParamMap": params}}
	path, method := "/v1.0/card/instances", "PUT"
	if r.I("sent_version") == 0 {
		method = "POST"
		path = "/v1.0/card/instances/createAndDeliver"
		payload["cardTemplateId"] = r.S("template_id")
		payload["callbackType"] = "STREAM"
		payload["openSpaceId"] = "dtv1.card//IM_GROUP." + r.S("conversation_id")
		payload["imGroupOpenSpaceModel"] = Row{"supportForward": false}
		payload["imGroupOpenDeliverModel"] = Row{"robotCode": d.Config.AppID}
	} else {
		payload["cardUpdateOptions"] = Row{"updateCardDataByKey": false}
	}
	out := Row{}
	if e := d.request(ctx, method, path, payload, &out); e != nil {
		return e
	}
	if e := confirmCardResponse(out); e != nil {
		return e
	}
	if method == "POST" {
		result, _ := out["result"].(map[string]any)
		deliveries, _ := result["deliverResults"].([]any)
		if len(deliveries) != 1 {
			return &UncertainEffectError{"钉钉创建响应缺少唯一群投递确认"}
		}
		delivery, _ := deliveries[0].(map[string]any)
		if delivery["success"] != true {
			// 实例可能已创建；不能将部分失败当成完全未发生而重新创建。
			return &UncertainEffectError{"钉钉实例可能已创建，群投递尚未确认"}
		}
	}
	return nil
}

// confirmCardResponse 同时核对 HTTP 成功之后的业务确认；缺失确认按未知副作用处理。
func confirmCardResponse(out Row) error {
	success, ok := out["success"].(bool)
	if !ok {
		return &UncertainEffectError{"钉钉响应缺少业务确认"}
	}
	if !success || out["result"] == false {
		return errors.New("钉钉未确认卡片同步")
	}
	return nil
}

// Topbox 有未完成项时创建或更新，归零时调用关闭接口。
func (d *DingTalk) Topbox(ctx context.Context, r Row) error {
	if d.Config.CoolAppCode == "" {
		return errors.New("未配置钉钉酷应用编码")
	}
	payload := Row{"outTrackId": r.S("out_track_id"), "openConversationId": r.S("conversation_id"), "conversationType": 1, "coolAppCode": d.Config.CoolAppCode, "robotCode": d.Config.AppID}
	path := "/v2.0/im/topBoxes/close"
	if r.S("desired_status") == "OPEN" {
		if r.S("template_id") == "" {
			return errors.New("未配置吊顶模板")
		}
		if r.S("observed_status") == "OPEN" {
			out := Row{}
			if e := d.request(ctx, "PUT", "/v1.0/card/instances", Row{"outTrackId": r.S("out_track_id"), "cardData": Row{"cardParamMap": CardParams(r)}}, &out); e != nil {
				return e
			}
			return confirmCardResponse(out)
		}
		path = "/v2.0/im/topBoxes"
		payload["cardTemplateId"] = r.S("template_id")
		payload["cardData"] = Row{"cardParamMap": CardParams(r)}
		payload["platforms"] = "ios|mac|android|win"
	}
	var out struct {
		Success bool `json:"success"`
	}
	if e := d.request(ctx, "POST", path, payload, &out); e != nil {
		return e
	}
	if !out.Success {
		return errors.New("钉钉未确认吊顶操作")
	}
	return nil
}

// CardParams 统一四类生产模板参数；MR 动态生成，超长列表提供完整 H5 入口。
func CardParams(r Row) map[string]string {
	status := map[string]string{"PENDING": "待处理", "PROCESSING": "处理中", "HANDLED": "已处理", "RECOVERED": "已恢复"}[r.S("round_status")]
	kind := r.S("card_type")
	heading := map[string]string{"LOG": "日志异常", "METRIC": "指标告警", "NOTICE": "业务提示"}[kind]
	body := strings.Builder{}
	if detail, ok := r["detail"].(Row); ok {
		round := detail["round"].(Row)
		fmt.Fprintf(&body, "**%s**\n\n项目：%s\n\n资源：%s\n\n告警编号：%s\n\n", round.S("title"), round.S("project_name"), round.S("resource_name"), round.S("alert_no"))
		if evidence, ok := detail["evidence"].(Row); ok {
			if kind == "LOG" {
				fmt.Fprintf(&body, "缺陷 ID：%s\n\n日志：%s\n\n", evidence.S("trace_id"), truncate(evidence.S("message"), 700))
			} else {
				value := evidence.S("metric_value_raw")
				if evidence.S("value_status") == "MISSING" {
					value = "未提供恢复值"
				}
				fmt.Fprintf(&body, "规则：%s\n\n指标值：%s\n\n", evidence.S("metric_rule"), value)
			}
		}
		fmt.Fprintf(&body, "发生次数：%d · %s\n\n", round.I("event_count"), round.S("count_quality"))
		if tasks, ok := detail["ai_tasks"].([]Row); ok && len(tasks) > 0 {
			fmt.Fprintf(&body, "AI：%s · %s\n\n", tasks[0].S("execution_status"), tasks[0].S("phase"))
		}
		if round.S("result_text") != "" {
			fmt.Fprintf(&body, "处理结果：%s\n\n", round.S("result_text"))
		}
		if records, ok := r["recent_records"].([]Row); ok {
			for _, v := range records {
				fmt.Fprintf(&body, "%s：%s\n\n", v.S("actor_name"), truncate(v.S("content"), 200))
			}
		}
		if mrs, ok := detail["merge_requests"].([]Row); ok {
			for i, m := range mrs {
				if i >= 8 {
					fmt.Fprintf(&body, "其余 %d 个合并请求请打开详情查看。\n", len(mrs)-8)
					break
				}
				fmt.Fprintf(&body, "[合并请求 #%s](%s) · %s\n\n", m.S("display_number"), m.S("url"), m.S("merge_status"))
			}
		}
	} else if notice, ok := r["notice"].(Row); ok {
		fmt.Fprintf(&body, "%s\n\n%s", notice.S("service_key"), truncate(notice.S("message"), 1500))
	} else {
		heading = "告警处理工作台"
		fmt.Fprintf(&body, "当前有 %d 项未完成告警\n\n待处理与处理中均计入", r.I("total"))
	}
	params := map[string]string{"title": heading, "status": status, "body": body.String(), "h5_url": dingtalkH5Link(r.S("h5_url")), "alert_no": r.S("alert_no"), "version": r.S("version"), "lastMessage": heading + " · " + status, "operation_key": digest(r.S("out_track_id") + ":" + r.S("version"))[:24]}
	for key, value := range presentationParams(r) {
		params[key] = value
	}
	for key, value := range params {
		params[key] = redactText(value)
	}
	return params
}

// safeStreamLogger 不输出可能包含凭据和完整事件的 SDK 调试文本。
type safeStreamLogger struct{}

func (safeStreamLogger) Debugf(string, ...interface{})   {}
func (safeStreamLogger) Infof(string, ...interface{})    {}
func (safeStreamLogger) Warningf(string, ...interface{}) {}
func (safeStreamLogger) Errorf(string, ...interface{})   {}
func (safeStreamLogger) Fatalf(string, ...interface{})   {}

var streamLoggerOnce sync.Once

// StartStream 将官方已认证连接的身份交给平台，没有公开的伪造卡片回调 HTTP 接口。
func (a *App) StartStream(ctx context.Context) error {
	d, ok := a.Identity.(*DingTalk)
	if !ok {
		return nil
	}
	streamLoggerOnce.Do(func() { logger.SetLogger(safeStreamLogger{}) })
	d.stream = client.NewStreamClient(client.WithAppCredential(client.NewAppCredentialConfig(d.Config.AppID, d.Config.AppSecret)))
	d.stream.RegisterCardCallbackRouter(a.HandleCardCallback)
	if e := d.stream.Start(ctx); e != nil {
		return errors.New("钉钉 Stream 连接失败")
	}
	return nil
}

// HandleCardCallback 只由官方 Stream 调用，重新核对平台卡片、企业身份及当前群成员。
func (a *App) HandleCardCallback(ctx context.Context, req *card.CardRequest) (*card.CardResponse, error) {
	if req == nil || req.CorpId != a.Config.CorpID || req.UserId == "" || req.UserIdType != 1 {
		return nil, fail(403, "卡片身份类型或企业不匹配")
	}
	instance, e := one(ctx, a.DB, "SELECT c.*,g.conversation_id FROM alert_card c JOIN alert_group g ON g.id=c.alert_group_id WHERE c.out_track_id=?", req.OutTrackId)
	if e != nil {
		return nil, e
	}
	if instance == nil || instance.I("alert_round_id") == 0 {
		return nil, fail(403, "卡片不是可处理的告警")
	}
	if req.SpaceId != instance.S("conversation_id") && req.SpaceId != "dtv1.card//IM_GROUP."+instance.S("conversation_id") {
		return nil, fail(403, "卡片来源群不匹配")
	}
	d, ok := a.Identity.(*DingTalk)
	if !ok {
		return nil, fail(403, "卡片回调需要真实钉钉身份适配器")
	}
	profile, e := d.legacy(ctx, "/topapi/v2/user/get", Row{"userid": req.UserId})
	if e != nil {
		return nil, e
	}
	if profile.S("userid") != req.UserId || profile.S("unionid") == "" {
		return nil, fail(403, "卡片操作者身份未通过验证")
	}
	u, e := a.login(ctx, Identity{CorpID: req.CorpId, AppID: a.Config.AppID, UserID: req.UserId, UnionID: profile.S("unionid"), Name: profile.S("name"), Avatar: profile.S("avatar")})
	if e != nil {
		return nil, e
	}
	action := req.GetActionString("action")
	kind := "NOTE"
	if action == "handle" {
		kind = "LOG_HANDLED"
	} else if action != "note" {
		return nil, fail(400, "不支持的卡片操作")
	}
	key := req.GetActionString("operation_key")
	if key == "" {
		return nil, fail(400, "卡片缺少操作幂等键")
	}
	_, e = a.humanAction(ctx, u, instance.I("alert_round_id"), "CARD", kind, digest(req.UserId+":"+key+":"+action), req.GetActionString("content"))
	if e != nil {
		return nil, e
	}
	return &card.CardResponse{UserPrivateData: &card.CardDataDto{CardParamMap: map[string]string{"operation_result": "已保存，卡片正在同步", "operation_key": a.signature(req.OutTrackId + ":" + req.UserId + ":" + key + ":next")[:32]}}}, nil
}

// dingtalkH5Link 分别编码 PC 侧栏和移动半浮层入口，普通浏览器仍可直接访问原始 H5 URL。
func dingtalkH5Link(raw string) string {
	if raw == "" {
		return ""
	}
	pc := "dingtalk://dingtalkclient/page/link?pc_slide=true&url=" + url.QueryEscape(raw)
	mobile := "dingtalk://dingtalkclient/action/im_open_hybrid_panel?panelHeight=percent83&hybridType=online&pageUrl=" + url.QueryEscape(raw)
	return "dingtalk://dingtalkclient/action/open_platform_link?pcLink=" + url.QueryEscape(pc) + "&mobileLink=" + url.QueryEscape(mobile)
}
