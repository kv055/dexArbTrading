package binance

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"

	"commons/cexclient"
	"github.com/binance/binance-connector-go/common/v2/common"
)

var (
	httpStatusRE  = regexp.MustCompile(`(?i)HTTP error (\d+)`)
	binanceCodeRE = regexp.MustCompile(`"code"\s*:\s*(-?\d+)`)
	binanceMsgRE  = regexp.MustCompile(`"msg"\s*:\s*"([^"\\]*(?:\\.[^"\\]*)*)"`)
)

// WrapError maps an official SDK failure into a redaction-safe cexclient.Error.
// Raw exchange response bodies from the SDK are never retained.
func WrapError(operation string, mutating bool, err error) error {
	if err == nil {
		return nil
	}
	out := &cexclient.Error{
		Provider:  "binance",
		Operation: operation,
		Message:   "request failed",
		Cause:     err,
	}

	switch {
	case errors.As(err, new(*common.TooManyRequestsError)):
		out.HTTPStatus = 429
		out.Retryable = true
		out.Message = "rate limited"
	case errors.As(err, new(*common.RateLimitBanError)):
		out.HTTPStatus = 418
		out.Retryable = true
		out.Message = "rate limit ban"
	case errors.As(err, new(*common.UnauthorizedError)):
		out.HTTPStatus = 401
		out.Message = "unauthorized"
	case errors.As(err, new(*common.ForbiddenError)):
		out.HTTPStatus = 403
		out.Message = "forbidden"
	case errors.As(err, new(*common.NotFoundError)):
		out.HTTPStatus = 404
		out.Message = "not found"
	case errors.As(err, new(*common.BadRequestError)):
		out.HTTPStatus = 400
		out.Message = "bad request"
		fillFromSDKMessage(out, err.Error())
	case errors.As(err, new(*common.ServerError)):
		out.HTTPStatus = 500
		out.Retryable = true
		out.Message = "server error"
		if status := parseHTTPStatus(err.Error()); status != 0 {
			out.HTTPStatus = status
		}
	case errors.As(err, new(*common.NetworkError)):
		out.Message = "network error"
		out.Retryable = true
	case errors.As(err, new(*common.ConnectorClientError)):
		out.Message = "connector error"
		fillFromSDKMessage(out, err.Error())
	default:
		msg := err.Error()
		// Keep short SDK/client messages; never retain JSON response bodies.
		if msg != "" && !strings.Contains(msg, "{") {
			out.Message = msg
		}
		fillFromSDKMessage(out, msg)
	}

	if out.HTTPStatus == 0 {
		out.HTTPStatus = parseHTTPStatus(err.Error())
	}
	if out.HTTPStatus == 429 || out.HTTPStatus >= 500 {
		out.Retryable = true
	}

	if mutating && (isTimeoutOrCancel(err) || errors.As(err, new(*common.NetworkError))) {
		out.AmbiguousMutation = true
		out.Retryable = true
	}
	return out
}

func fillFromSDKMessage(out *cexclient.Error, raw string) {
	if status := parseHTTPStatus(raw); status != 0 {
		out.HTTPStatus = status
	}
	if m := binanceCodeRE.FindStringSubmatch(raw); len(m) == 2 {
		out.Code = m[1]
	}
	if m := binanceMsgRE.FindStringSubmatch(raw); len(m) == 2 {
		// Unescape only the short exchange msg field; never keep the raw body.
		out.Message = strings.ReplaceAll(m[1], `\"`, `"`)
	}
}

func parseHTTPStatus(raw string) int {
	if m := httpStatusRE.FindStringSubmatch(raw); len(m) == 2 {
		status, err := strconv.Atoi(m[1])
		if err == nil {
			return status
		}
	}
	if strings.Contains(raw, "Server error:") {
		parts := strings.Split(raw, "Server error:")
		if len(parts) > 1 {
			status, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err == nil {
				return status
			}
		}
	}
	return 0
}

func isTimeoutOrCancel(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context canceled")
}
