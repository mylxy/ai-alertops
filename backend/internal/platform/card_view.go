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
	for _, key := range []string{"log_count", "metric_count", "total_count"} {
		p["runtime_"+key] = p[key]
	}
	completeDesignParams(view, p)
	return p
}

// completeDesignParams 为原设计的独立信息块提供真实字段，缺失来源值不使用演示值补齐。
func completeDesignParams(view Row, p map[string]string) {
	p["has_trace"], p["notes_empty"], p["has_notes"], p["has_mrs"] = "no", "yes", "no", "no"
	p["history_label"] = "查看完整处理记录  ↗"
	for n := 1; n <= 3; n++ {
		p[fmt.Sprintf("note_%d_visible", n)] = "no"
	}
	for n := 1; n <= 8; n++ {
		for _, f := range []string{"label", "status", "url", "visible"} {
			p[fmt.Sprintf("mr_%d_%s", n, f)] = ""
		}
	}
	if d, ok := view["detail"].(Row); ok {
		r := d["round"].(Row)
		p["project"], p["service"] = r.S("project_name"), r.S("resource_name")
		p["duration"] = fmt.Sprintf("%d 秒", max(int64(0), r.I("last_event_time")-r.I("first_event_time"))/1000)
		p["first_seen"], p["latest_seen"] = cardTime(r.I("first_event_time")), cardTime(r.I("last_event_time"))
		p["progress_heading"], p["progress_body"] = "等待 AI 开始处理", ""
		if r.S("round_status") == "PROCESSING" {
			p["progress_heading"] = "正在处理"
		}
		records, _ := d["records"].([]Row)
		// 记录按时间正序返回，最后一条自动进展是当前可验证的 AI 说明。
		for _, record := range records {
			if record.S("actor_type") == "AI" {
				p["progress_body"] = truncate(record.S("content"), 1500)
			}
		}
		if p["ai_summary"] != "" {
			p["progress_body"] = strings.TrimSpace(p["ai_summary"] + "\n" + p["progress_body"])
		}
		if r.S("round_status") == "HANDLED" {
			p["progress_heading"], p["progress_body"] = "人工处理完成", r.S("result_text")
		}
		if e, ok := d["evidence"].(Row); ok {
			p["trace_id"], p["log_excerpt"] = e.S("trace_id"), truncate(e.S("message"), 1000)
			if p["trace_id"] != "" {
				p["has_trace"] = "yes"
			}
			if r.S("alert_type") == "LOG" {
				p["service"] = e.S("service_key")
			}
			p["metric_value"], p["metric_rule"], p["metric_key"] = e.S("metric_value_raw"), e.S("metric_rule"), e.S("metric_key")
			p["metric_color"], p["monitor_hint"] = "common_red1_color", "监控仍处于告警状态，等待有效恢复事件。"
			if r.S("round_status") == "RECOVERED" {
				p["metric_color"], p["monitor_hint"] = "common_green1_color", "监控已确认恢复，本轮告警自动结束。"
			}
			if e.S("value_status") == "MISSING" {
				p["metric_value"] = "上游未提供恢复值"
			}
			if p["metric_rule"] == "" {
				p["metric_rule"] = "上游未提供阈值或持续条件"
			}
		}
		if p["note_count"] != "0" {
			p["notes_empty"], p["has_notes"] = "no", "yes"
		}
		for n := 1; n <= 3; n++ {
			if p[fmt.Sprintf("note_%d_meta", n)] != "" {
				p[fmt.Sprintf("note_%d_visible", n)] = "yes"
			}
		}
		mrs, _ := d["merge_requests"].([]Row)
		merged := 0
		for n, m := range mrs {
			if m.S("merge_status") == "MERGED" {
				merged++
			}
			if n >= 8 {
				continue
			}
			prefix := fmt.Sprintf("mr_%d_", n+1)
			p[prefix+"label"] = m.S("repository_id") + " #" + m.S("display_number") + "  ↗"
			p[prefix+"status"] = map[string]string{"OPEN": "待合并", "MERGED": "已合并", "CLOSED": "关闭未合并"}[m.S("merge_status")]
			p[prefix+"url"], p[prefix+"visible"] = m.S("url"), "yes"
		}
		if len(mrs) > 0 {
			p["has_mrs"] = "yes"
		}
		p["mr_progress"] = fmt.Sprintf("%d/%d 已合并", merged, len(mrs))
		if len(mrs) > 8 {
			p["mr_progress"] += " · 完整列表见处理记录"
		}
	} else if n, ok := view["notice"].(Row); ok {
		p["project"], p["service"], p["message"] = n.S("project_name"), n.S("service_key"), n.S("message")
	}
}
