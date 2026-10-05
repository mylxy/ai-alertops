package platform_test

import (
	"fmt"
	"testing"

	"github.com/mylxy/ai-alertops/backend/internal/platform"
)

// TestCompleteCardPresentation 验证完整设计字段来自业务事实，不使用预览值补齐缺失证据。
func TestCompleteCardPresentation(t *testing.T) {
	t.Run("指标恢复缺值时明确提示且不沿用异常值", func(t *testing.T) {
		// 准备：监控确认恢复，但没有提供恢复数值。
		round := platform.Row{"alert_type": "METRIC", "round_status": "RECOVERED", "event_count": 2}
		view := platform.Row{"card_type": "METRIC", "round_status": "RECOVERED", "detail": platform.Row{"round": round, "evidence": platform.Row{"metric_value_raw": "95%", "value_status": "MISSING"}}}
		// 执行：构建实际发送的卡片参数。
		p := platform.CardParams(view)
		// 验证：状态、数值与颜色同属恢复状态，不能误显示历史异常值。
		for k, want := range map[string]string{"status": "已恢复", "metric_value": "上游未提供恢复值", "phase": "green", "is_open": "no", "metric_color": "common_green1_color"} {
			if p[k] != want {
				t.Errorf("%s = %q，期望 %q", k, p[k], want)
			}
		}
	})
	t.Run("十个合并请求汇总完整并提供八个独立入口", func(t *testing.T) {
		// 准备：十个独立 MR，其中前三个已合并。
		mrs := []platform.Row{}
		for n := 1; n <= 10; n++ {
			status := "OPEN"
			if n <= 3 {
				status = "MERGED"
			}
			mrs = append(mrs, platform.Row{"repository_id": "repo", "display_number": fmt.Sprint(n), "url": fmt.Sprintf("https://codeup.aliyun.com/test/repo/change/%d", n), "merge_status": status})
		}
		view := platform.Row{"card_type": "LOG", "detail": platform.Row{"round": platform.Row{"round_status": "PROCESSING", "event_count": 1}, "merge_requests": mrs}}
		// 执行：生成原设计中的逐行 MR 字段。
		p := platform.CardParams(view)
		// 验证：汇总没有把展示上限当成业务总数，各链接与状态独立。
		if p["mr_progress"] != "3/10 已合并 · 完整列表见处理记录" {
			t.Errorf("MR 汇总错误：%q", p["mr_progress"])
		}
		for n := 1; n <= 8; n++ {
			key := fmt.Sprintf("mr_%d_url", n)
			if p[key] != mrs[n-1].S("url") {
				t.Errorf("第 %d 个 MR 入口错误：%q", n, p[key])
			}
		}
		if p["mr_1_status"] != "已合并" || p["mr_4_status"] != "待合并" {
			t.Errorf("独立 MR 状态错误：%v", p)
		}
	})
}
