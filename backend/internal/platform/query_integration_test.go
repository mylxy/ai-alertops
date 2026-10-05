//go:build integration

package platform_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"
)

// TestQueryPaginationAndStatistics 验证同毫秒 51 轮次分页与未发送卡片的统计口径。
func TestQueryPaginationAndStatistics(t *testing.T) {
	// 准备：通过 Hook 创建同一时间的 51 个不同问题，暂不投递外部卡片。
	app, server := testPlatform(t)
	_, _, cookies := requestJSON(t, server, "POST", "/api/auth/demo", map[string]string{"identity": "member"}, nil, nil)
	for n := range 51 {
		code, body, _ := requestJSON(t, server, "POST", "/hook/sls", map[string]string{"namespace_name": "demo", "container_name": "api", "class": "Service", "level": "ERROR", "message": fmt.Sprintf("独立异常-%d", n), "@timestamp": "2026-10-05T01:00:00Z"}, nil, map[string]string{"X-Alert-Source": "demo-log", "Authorization": "Bearer demo-hook-secret"})
		if code != 200 {
			t.Fatal(body)
		}
	}
	if e := app.ProcessEvents(context.Background(), 100); e != nil {
		t.Fatal(e)
	}
	// 执行：第一页后追加查询第二页，采用时间加 ID 游标。
	_, first, _ := requestJSON(t, server, "GET", "/api/rounds", nil, cookies[0], nil)
	cursor := first["next_cursor"].(string)
	if len(first["items"].([]any)) != 50 || cursor == "" {
		t.Fatal("分页第一批错误")
	}
	_, second, _ := requestJSON(t, server, "GET", "/api/rounds?cursor="+url.QueryEscape(cursor), nil, cookies[0], nil)
	if len(second["items"].([]any)) != 1 || second["next_cursor"] != "" {
		t.Fatal("分页第二批错误")
	}
	seen := map[string]bool{}
	for _, page := range []map[string]any{first, second} {
		for _, item := range page["items"].([]any) {
			id := item.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatal("游标重复返回同一轮次")
			}
			seen[id] = true
		}
	}
	// 验证：期间左闭右开；没有发送成功也按轮次计数，新增备注卡不能增加待办。
	id := second["items"].([]any)[0].(map[string]any)["id"].(string)
	code, body, _ := requestJSON(t, server, "POST", "/api/h5/rounds/"+id+"/notes", map[string]string{"content": "首次备注", "operation_key": "page-note"}, cookies[0], nil)
	if code != 200 {
		t.Fatal(body)
	}
	for _, tc := range []struct {
		query   string
		created float64
	}{{"from=1791162000000&to=1791162000001", 51}, {"from=1791161999999&to=1791162000000", 0}} {
		_, stats, _ := requestJSON(t, server, "GET", "/api/stats?"+tc.query, nil, cookies[0], nil)
		item := stats["items"].([]any)[0].(map[string]any)
		// MySQL 聚合 DECIMAL 由 JSON 字符串保留精度。
		if fmt.Sprint(item["unfinished"]) != "51" || fmt.Sprint(item["created"]) != fmt.Sprint(tc.created) {
			t.Fatal(stats)
		}
	}
}

// TestSchemaContract 核对目标 MySQL 的实际结构，不以文档声明替代约束验证。
func TestSchemaContract(t *testing.T) {
	// 准备：测试入口在 MySQL 执行初始 DDL。
	app, _ := testPlatform(t)
	// 执行和验证：表数量、存储设置、主键/字段和索引前缀均从数据库读取。
	var count int
	checks := []struct {
		query string
		want  int
	}{
		{"SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND ENGINE='InnoDB' AND TABLE_COLLATION='utf8mb4_bin' AND TABLE_COMMENT<>''", 23},
		{"SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND (IS_NULLABLE<>'NO' OR EXTRA LIKE '%auto_increment%' OR COLUMN_COMMENT='')", 0},
		{"SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND COLUMN_NAME='id' AND COLUMN_TYPE='bigint' AND COLUMN_KEY='PRI'", 23},
		{"SELECT COUNT(*) FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_SCHEMA=DATABASE()", 0},
		{"SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND INDEX_NAME<>'PRIMARY' AND INDEX_NAME NOT LIKE 'uk_%' AND INDEX_NAME NOT LIKE 'idx_%'", 0},
	}
	for _, check := range checks {
		if e := app.DB.QueryRow(check.query).Scan(&count); e != nil || count != check.want {
			t.Fatalf("结构不符合约定：%d want %d %v", count, check.want, e)
		}
	}
	if _, e := app.DB.Exec("UPDATE alert_project_workspace SET slot_no=4 LIMIT 1"); e == nil {
		t.Fatal("数据库接受了第四槽位")
	}
}
