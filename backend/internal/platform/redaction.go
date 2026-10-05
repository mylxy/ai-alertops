package platform

import (
	"encoding/json"
	"regexp"
	"strings"
)

var credentialText = regexp.MustCompile(`(?i)(password|passwd|api[_-]?key|access[_-]?token|client[_-]?secret|secret)(["\']?\s*[=:]\s*["']?)([^\s"',;}]+)`)
var bearerText = regexp.MustCompile(`(?i)Bearer\s+[a-z0-9._~+/=-]+`)

// redactText 只脱敏凭据上下文，保留告警编号、Trace ID 与故障证据主体。
func redactText(s string) string {
	return bearerText.ReplaceAllString(credentialText.ReplaceAllString(s, "${1}${2}[REDACTED]"), "Bearer [REDACTED]")
}

// redact 在公开响应和 AI 输入边界递归脱敏；持久分组摘要仍基于原始业务消息。
func redact(v any) any {
	switch x := v.(type) {
	case Row:
		out := Row{}
		for k, v := range x {
			switch strings.ToLower(k) {
			case "password", "passwd", "token", "access_token", "accesstoken", "client_secret", "clientsecret", "api_key", "apikey", "execution_token_hash":
				out[k] = "[REDACTED]"
			default:
				out[k] = redact(v)
			}
		}
		return out
	case map[string]any:
		return redact(Row(x))
	case []Row:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = redact(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = redact(v)
		}
		return out
	case string:
		return redactText(x)
	case json.Number:
		return x
	default:
		return v
	}
}
