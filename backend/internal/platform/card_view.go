package platform

import (
	"fmt"
	"strings"
	"time"
)

// cardTime 固定上海时区，不把缺失来源时间显示为 1970 年。
func cardTime(ms int64) string {
	if ms <= 0 {
		return "未提供"
	}
	return time.UnixMilli(ms).In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04:05")
}

// presentationParams 将四类已确认卡片的显示字段集中映射，不在模板内推断业务状态。
func presentationParams(view Row) map[string]string {
	p := map[string]string{"phase": "red", "is_open": "no", "note_count": "0", "ai_summary": "", "mr_markdown": "", "result": "", "context": "", "time_summary": "", "evidence": "", "card_title": view.S("title"), "kind": view.S("card_type")}
	for n := 1; n <= 3; n++ {
		p[fmt.Sprintf("note_%d_meta", n)] = ""
		p[fmt.Sprintf("note_%d_body", n)] = ""
	}
	if d, ok := view["detail"].(Row); ok {
		r := d["round"].(Row)
		p["card_title"] = r.S("title")
		p["context"] = r.S("project_name") + " / " + r.S("resource_name")
		p["result"] = r.S("result_text")
		p["count"] = r.S("event_count")
		p["alert_no"] = r.S("alert_no")
		quality := "准确计数"
		if r.S("count_quality") != "EXACT" {
			quality = "尽力计数"
		}
		p["time_summary"] = fmt.Sprintf("累计 %d 次 · %s\n首次 %s\n最近 %s\n持续 %d 秒", r.I("event_count"), quality, cardTime(r.I("first_event_time")), cardTime(r.I("last_event_time")), max(int64(0), r.I("last_event_time")-r.I("first_event_time"))/1000)
		switch r.S("round_status") {
		case "PENDING":
			p["is_open"] = "yes"
		case "PROCESSING":
			p["is_open"] = "yes"
			p["phase"] = "orange"
		default:
			p["phase"] = "green"
			p["time_summary"] += "\n结束 " + cardTime(r.I("end_time"))
		}
		if e, ok := d["evidence"].(Row); ok {
			if r.S("alert_type") == "LOG" {
				trace := e.S("trace_id")
				if trace == "" {
					trace = "上游未提供 Trace ID"
				}
				p["evidence"] = "缺陷 ID：" + trace + "\n异常类：" + e.S("class_name") + "\n" + truncate(e.S("message"), 1000)
			} else {
				value := e.S("metric_value_raw")
				if e.S("value_status") == "MISSING" {
					value = "上游未提供恢复值"
				}
				p["evidence"] = "规则：" + e.S("metric_rule") + "\n指标：" + e.S("metric_key") + "\n当前值：" + value
			}
		}
		if tasks, ok := d["ai_tasks"].([]Row); ok && len(tasks) > 0 {
			t := tasks[0]
			labels := map[string]string{"QUEUED": "排队中", "STARTING": "启动中", "RUNNING": "执行中", "FINALIZING": "收尾中", "SUCCEEDED": "执行完成", "FAILED": "执行失败", "SKIPPED": "已跳过", "DIAGNOSING": "排查中", "LOCATED": "定位成功", "REPAIRING": "修复中", "VERIFYING": "验证中", "WAITING": "等待执行", "FINISHED": "已结束"}
			p["ai_summary"] = "AI：" + labels[t.S("execution_status")] + " · " + labels[t.S("phase")]
			if t.S("branch_name") != "" {
				p["ai_summary"] += "\n分支：" + t.S("branch_name")
			}
			if t.S("error_message") != "" {
				p["ai_summary"] += "\n" + truncate(t.S("error_message"), 300)
			}
		}
		records, _ := d["records"].([]Row)
		notes := []Row{}
		for _, record := range records {
			if record.S("record_type") == "NOTE" {
				notes = append(notes, record)
			}
		}
		p["note_count"] = fmt.Sprint(len(notes))
		for n := 1; n <= 3 && n <= len(notes); n++ {
			note := notes[len(notes)-n]
			p[fmt.Sprintf("note_%d_meta", n)] = note.S("actor_name") + " · " + cardTime(note.I("occurred_time"))
			p[fmt.Sprintf("note_%d_body", n)] = truncate(note.S("content"), 300)
		}
		mrs, _ := d["merge_requests"].([]Row)
		var md strings.Builder
		for n, m := range mrs {
			if n >= 8 {
				fmt.Fprintf(&md, "其余 %d 条请查看完整详情。", len(mrs)-8)
				break
			}
			status := map[string]string{"OPEN": "待合并", "CLOSED": "关闭未合并", "MERGED": "已合并"}[m.S("merge_status")]
			fmt.Fprintf(&md, "[合并请求 #%s](%s) · %s\n\n", m.S("display_number"), m.S("url"), status)
		}
		if all, ok := d["all_merged"].(bool); ok && all {
			md.WriteString("必要修复分支全部已合并；告警仍需人工确认。")
		}
		p["mr_markdown"] = md.String()
	} else if n, ok := view["notice"].(Row); ok {
		p["phase"] = "blue"
		p["card_title"] = "业务提示"
		p["context"] = n.S("project_name") + " / " + n.S("service_key")
		p["evidence"] = truncate(n.S("message"), 1500)
	} else {
		p["kind"] = "TOPBOX"
		p["total_count"] = view.S("total")
		for _, key := range []string{"log_count", "log_pending", "log_processing", "metric_count", "metric_pending", "metric_processing"} {
			p[key] = "0"
		}
		counts, _ := view["counts"].([]Row)
		totals := map[string]int64{}
		for _, c := range counts {
			typ := strings.ToLower(c.S("alert_type"))
			totals[typ] += c.I("total")
			p[typ+"_"+strings.ToLower(c.S("round_status"))] = c.S("total")
		}
		for typ, v := range totals {
			p[typ+"_count"] = fmt.Sprint(v)
		}
	}
	return p
}
