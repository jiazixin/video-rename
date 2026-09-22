package vision

import (
	"time"

	"video-rename/internal/domain"
)

// httpJobError 在 domain.JobError 之上携带重试控制信息。
type httpJobError struct {
	domain.JobError
	RetryableFlag bool          // 是否按重试策略重试
	RetryAfter    time.Duration // 服务端 Retry-After 要求的最小等待
}

// Retryable 报告该错误是否进入有限重试。
// 取消、鉴权失效、协议类错误与资源超限均不重试。
func (e *httpJobError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.Code {
	case domain.CodeCancelled, domain.CodeAPIUnauthorized,
		domain.CodeAPIResponseInvalid, domain.CodeAPIResponseTooLarge,
		domain.CodeModelOutputTruncated, domain.CodeTitleInvalid, domain.CodeNamingFailed,
		domain.CodeRequestTooLarge:
		return false
	}
	if e.RetryableFlag {
		return true
	}
	// 传输层错误(超时、连接重置、EOF 等)在 Title 循环里默认可重试
	return e.HTTPStatus == 0 && e.Code != domain.CodeAPIUnauthorized
}
