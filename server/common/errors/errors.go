package errors

import (
	stderr "errors"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/superdurable/dex/server/common/utils/caller"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CategorizedError provides category to errors and wrapping the from error with detailed messages
type CategorizedError interface {
	error
	// GetFullError will include the from error message
	// (usually for logging only, cuz we don't do for API request(to avoid leaking internal details)
	GetFullError() string
	GetFromError() error
	GetCategory() ErrorCategory
	GetSubCategory() string
	GetCallAtPath() string
	GetHttpStatusCode() int

	IsInternalError() bool
	IsTimeoutError() bool
	IsRetriable() bool
	IsNotFoundError() bool
	IsConflictError() bool
	IsInvalidInputError() bool
	IsResourceExhaustedError() bool
	IsCASError() bool
	// IsRetriableExcludingCAS is true when IsRetriable() would be true except
	// CAS (compare-and-swap / version mismatch) errors: those must not be retried
	// without a fresh read. Used for lease Claim/Renew and similar optimistic-lock retries.
	IsRetriableExcludingCAS() bool

	ToJSON() ([]byte, error)
}

type ErrorCategory string

const (
	ErrorCategoryInternal          ErrorCategory = "internal"
	ErrorCategoryNotFound          ErrorCategory = "not_found"
	ErrorCategoryConflict          ErrorCategory = "conflict"
	ErrorCategoryInvalidInput      ErrorCategory = "invalid_input"
	ErrorCategoryUnauthenticated   ErrorCategory = "unauthenticated"
	ErrorCategoryPermissionDenied  ErrorCategory = "permission_denied"
	ErrorCategoryRateLimited       ErrorCategory = "rate_limited"
	ErrorCategoryUnavailable       ErrorCategory = "unavailable"
	ErrorCategoryTimeout           ErrorCategory = "timeout"
	ErrorCategoryUnimplemented     ErrorCategory = "unimplemented"
	ErrorCategoryCanceled          ErrorCategory = "cancel"
	ErrorCategoryResourceExhausted ErrorCategory = "resource_exhausted"
	ErrorCategoryCAS               ErrorCategory = "cas_mismatch"
)

// CategoryHasSubCategories reports whether the given category defines typed sub-categories.
func CategoryHasSubCategories(cat ErrorCategory) bool {
	switch cat {
	case ErrorCategoryInternal, ErrorCategoryInvalidInput, ErrorCategoryCanceled:
		return true
	default:
		return false
	}
}

type errorSubCategory string

// AsCategorizedError attempts to cast an error to a CategorizedError.
// Returns the CategorizedError and a boolean indicating if the cast was successful.
// If the cast is not successful, returns a new InternalError with the original error as the from error.
func AsCategorizedError(err error) (CategorizedError, bool) {
	var catErr CategorizedError
	if stderr.As(err, &catErr) {
		return catErr, true
	}
	return newInternalErrorWithAdditionalSkip("not a categorized error(need to improve error handling)", err), false
}

func IsRetriableError(err error) bool {
	catErr, _ := AsCategorizedError(err)
	return catErr.IsRetriable()
}

// IsRetriableExcludingCASError reports whether err is a CategorizedError for which
// a blind retry may help (transient internal / timeout / unavailable). Returns
// false for nil, uncategorized errors, and CAS failures (version mismatch).
func IsRetriableExcludingCASError(err error) bool {
	if err == nil {
		return false
	}
	var catErr CategorizedError
	if !stderr.As(err, &catErr) {
		return false
	}
	return catErr.IsRetriableExcludingCAS()
}

// ToProtoError maps the internal errors to their corresponding Protobuf codes.
func ToProtoError(err error) error {
	if catErr, ok := err.(CategorizedError); ok {
		msg := catErr.Error()

		switch catErr.GetCategory() {
		case ErrorCategoryUnauthenticated:
			return status.Error(codes.Unauthenticated, msg)
		case ErrorCategoryPermissionDenied:
			return status.Error(codes.PermissionDenied, msg)
		case ErrorCategoryNotFound:
			return status.Error(codes.NotFound, msg)
		case ErrorCategoryConflict:
			return status.Error(codes.AlreadyExists, msg)
		case ErrorCategoryInvalidInput:
			bytes, er := catErr.ToJSON()
			if er != nil {
				return status.Error(codes.Internal, er.Error())
			}
			return status.Error(codes.InvalidArgument, string(bytes))
		case ErrorCategoryRateLimited:
			return status.Error(codes.ResourceExhausted, msg)
		case ErrorCategoryTimeout:
			return status.Error(codes.DeadlineExceeded, msg)
		case ErrorCategoryCanceled:
			return status.Error(codes.Canceled, msg)
		case ErrorCategoryUnavailable:
			return status.Error(codes.Unavailable, msg)
		case ErrorCategoryUnimplemented:
			return status.Error(codes.Unimplemented, msg)
		case ErrorCategoryResourceExhausted:
			return status.Error(codes.ResourceExhausted, msg)
		case ErrorCategoryCAS:
			return status.Error(codes.Aborted, msg)
		default:
			return status.Error(codes.Internal, msg)
		}
	}
	return status.Errorf(codes.Internal, "unknown server error")
}

type categorizedErrorImpl struct {
	from        error
	message     string
	category    ErrorCategory
	httpCode    *int
	errorAt     string
	subCategory errorSubCategory
}

const skipForCallAt = 2

var _ CategorizedError = (*categorizedErrorImpl)(nil)

func newInternalErrorWithAdditionalSkip(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryInternal,
		errorAt:  caller.PathToCaller(skipForCallAt + 1),
	}
}

func NewInternalError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryInternal,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewInternalErrorWithSubCategory(msg string, fromErr error, subCategory string) CategorizedError {
	return &categorizedErrorImpl{
		from:        fromErr,
		message:     msg,
		category:    ErrorCategoryInternal,
		subCategory: errorSubCategory(subCategory),
		errorAt:     caller.PathToCaller(skipForCallAt),
	}
}

func NewResourceExhaustedError(msg string) CategorizedError {
	return &categorizedErrorImpl{
		message:  msg,
		category: ErrorCategoryResourceExhausted,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewNotFoundError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryNotFound,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewConflictError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryConflict,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewCASError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryCAS,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewInvalidInputError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryInvalidInput,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewInvalidInputErrorWithSubCategory(msg string, subCategory string) CategorizedError {
	return &categorizedErrorImpl{
		message:     msg,
		category:    ErrorCategoryInvalidInput,
		subCategory: errorSubCategory(subCategory),
		errorAt:     caller.PathToCaller(skipForCallAt),
	}
}

func NewTimeoutError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryTimeout,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewUnavailableError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryUnavailable,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewUnimplementedError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryUnimplemented,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewCancelError(msg string, fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  msg,
		category: ErrorCategoryCanceled,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewCancelErrorWithSubCategory(msg string, subCategory string) CategorizedError {
	return &categorizedErrorImpl{
		message:     msg,
		category:    ErrorCategoryCanceled,
		subCategory: errorSubCategory(subCategory),
		errorAt:     caller.PathToCaller(skipForCallAt),
	}
}

func NewContextCanceledError(fromErr error) CategorizedError {
	return &categorizedErrorImpl{
		from:     fromErr,
		message:  "context canceled",
		category: ErrorCategoryCanceled,
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func NewFromHTTPStatus(code int, body []byte) CategorizedError {
	category := categoryFromHTTP(code)
	msg := errSummary(category)
	return &categorizedErrorImpl{
		message:  msg,
		category: category,
		httpCode: &code,
		from:     fmt.Errorf("http %d: %s", code, formatHTTPBody(body, defaultMaxHttpBodySize)),
		errorAt:  caller.PathToCaller(skipForCallAt),
	}
}

func (c *categorizedErrorImpl) Error() string {
	return c.message
}

func marshalErrorWithCode(error string, code string, message string) []byte {
	b, _ := json.Marshal(map[string]string{"error": error, "code": code, "message": message})
	return b
}

func (c *categorizedErrorImpl) ToJSON() ([]byte, error) {
	if c.subCategory != "" {
		return marshalErrorWithCode(string(c.subCategory), CanonicalCode(string(c.subCategory)), c.GetFullError()), nil
	}
	return marshalErrorWithCode(string(c.category), CanonicalCode(string(c.category)), c.GetFullError()), nil
}

func (c *categorizedErrorImpl) GetHttpStatusCode() int {
	if c.httpCode != nil {
		return *c.httpCode
	}
	return 0
}

func (c *categorizedErrorImpl) GetFullError() string {
	parts := []string{string(c.category) + ": " + c.message}

	if c.httpCode != nil {
		parts = append(parts, fmt.Sprintf("(http=%d)", *c.httpCode))
	}
	if c.from != nil {
		if catErr, ok := c.from.(CategorizedError); ok {
			parts = append(parts, fmt.Sprintf("from: %s", catErr.GetFullError()))
		} else {
			parts = append(parts, fmt.Sprintf("from: %v", c.from))
		}
	}
	return strings.Join(parts, "; ")
}

func (c *categorizedErrorImpl) GetCallAtPath() string {
	return c.errorAt
}

func (c *categorizedErrorImpl) GetFromError() error {
	return c.from
}

func (c *categorizedErrorImpl) GetCategory() ErrorCategory {
	return c.category
}

func (c *categorizedErrorImpl) GetSubCategory() string {
	return string(c.subCategory)
}

func (c *categorizedErrorImpl) IsRetriable() bool {
	switch c.category {
	case ErrorCategoryTimeout, ErrorCategoryUnavailable, ErrorCategoryInternal, ErrorCategoryCAS:
		return true
	default:
		return false
	}
}

func (c *categorizedErrorImpl) IsInternalError() bool {
	return c.category == ErrorCategoryInternal
}

func (c *categorizedErrorImpl) IsTimeoutError() bool {
	return c.category == ErrorCategoryTimeout
}

func (c *categorizedErrorImpl) IsNotFoundError() bool {
	return c.category == ErrorCategoryNotFound
}

func (c *categorizedErrorImpl) IsConflictError() bool {
	return c.category == ErrorCategoryConflict
}

func (c *categorizedErrorImpl) IsInvalidInputError() bool {
	return c.category == ErrorCategoryInvalidInput
}

func (c *categorizedErrorImpl) IsResourceExhaustedError() bool {
	return c.category == ErrorCategoryResourceExhausted
}

func (c *categorizedErrorImpl) IsCASError() bool {
	return c.category == ErrorCategoryCAS
}

func (c *categorizedErrorImpl) IsRetriableExcludingCAS() bool {
	if c.IsCASError() {
		return false
	}
	return c.IsRetriable()
}

func categoryFromHTTP(code int) ErrorCategory {
	switch code {
	case http.StatusUnauthorized:
		return ErrorCategoryUnauthenticated
	case http.StatusForbidden, 451:
		return ErrorCategoryPermissionDenied
	case http.StatusNotFound, http.StatusGone:
		return ErrorCategoryNotFound
	case http.StatusConflict, http.StatusPreconditionFailed, http.StatusLocked:
		return ErrorCategoryConflict
	case http.StatusTooManyRequests:
		return ErrorCategoryRateLimited
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ErrorCategoryTimeout
	case http.StatusNotImplemented:
		return ErrorCategoryUnimplemented
	case http.StatusBadGateway, http.StatusServiceUnavailable, 520, 521, 522, 523:
		return ErrorCategoryUnavailable
	case http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusNotAcceptable,
		http.StatusLengthRequired, http.StatusRequestEntityTooLarge, http.StatusRequestURITooLong,
		http.StatusUnsupportedMediaType, http.StatusRequestedRangeNotSatisfiable, http.StatusTeapot,
		http.StatusUnprocessableEntity, http.StatusUpgradeRequired, http.StatusPreconditionRequired,
		http.StatusRequestHeaderFieldsTooLarge:
		return ErrorCategoryInvalidInput
	case http.StatusInternalServerError:
		return ErrorCategoryInternal
	default:
		switch {
		case code >= 500 && code <= 599:
			return ErrorCategoryUnavailable
		case code >= 400 && code <= 499:
			return ErrorCategoryInvalidInput
		default:
			return ErrorCategoryInternal
		}
	}
}

const defaultMaxHttpBodySize = 512

func formatHTTPBody(body []byte, maxOut int) string {
	origLen := len(body)
	if maxOut == 0 {
		maxOut = defaultMaxHttpBodySize
	}

	var buf []byte
	for i, w := 0, 0; i < len(body) && len(buf) < maxOut; i += w {
		r, size := utf8.DecodeRune(body[i:])
		w = size
		if r == utf8.RuneError && size == 1 {
			break
		}
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			continue
		}
		buf = utf8.AppendRune(buf, r)
	}

	if len(buf) > 0 {
		s := strings.TrimSpace(string(buf))
		if origLen > maxOut {
			return s + "...(truncated)"
		}
		return s
	}

	return fmt.Sprintf("binary body (%d bytes)", origLen)
}

func errSummary(cat ErrorCategory) string {
	switch cat {
	case ErrorCategoryUnauthenticated:
		return "authentication required"
	case ErrorCategoryPermissionDenied:
		return "permission denied"
	case ErrorCategoryNotFound:
		return "resource not found"
	case ErrorCategoryConflict:
		return "conflict"
	case ErrorCategoryInvalidInput:
		return "invalid input"
	case ErrorCategoryRateLimited:
		return "rate limit exceeded"
	case ErrorCategoryTimeout:
		return "request timed out"
	case ErrorCategoryCanceled:
		return "request canceled"
	case ErrorCategoryUnavailable:
		return "service unavailable"
	case ErrorCategoryUnimplemented:
		return "unimplemented"
	case ErrorCategoryInternal:
		return "internal error"
	case ErrorCategoryCAS:
		return "CAS mismatch"
	default:
		return "unknown error"
	}
}

func CombineAsInternalErrors(errs ...CategorizedError) CategorizedError {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	combinedMsg := countAndCombine(messages, "; ")

	errorAts := make([]string, 0, len(errs))
	for _, err := range errs {
		errorAts = append(errorAts, err.GetCallAtPath())
	}
	combinedErrorAt := countAndCombine(errorAts, "; ")

	errFmt := ""
	fromErrs := make([]any, 0, len(errs))
	for _, err := range errs {
		if err.GetFromError() != nil {
			errFmt += " %w"
			fromErrs = append(fromErrs, err.GetFromError())
		}
	}
	var combinedFromErr error
	if len(fromErrs) > 0 {
		combinedFromErr = fmt.Errorf(errFmt, fromErrs...)
	}

	return &categorizedErrorImpl{
		from:        combinedFromErr,
		message:     combinedMsg,
		category:    ErrorCategoryInternal,
		errorAt:     combinedErrorAt,
		subCategory: errorSubCategory(errs[0].GetSubCategory()),
	}
}

func CanonicalCode(name string) string {
	return toUpperSnake(name)
}

func toUpperSnake(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if r == ' ' || r == '-' {
			b.WriteByte('_')
			prev = r
			continue
		}
		if i > 0 && ((unicode.IsLower(prev) && unicode.IsUpper(r)) || (unicode.IsDigit(prev) && unicode.IsLetter(r))) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
		prev = r
	}
	return b.String()
}

func countAndCombine(items []string, separator string) string {
	if len(items) == 0 {
		return ""
	}

	counts := make(map[string]int)
	order := []string{}

	for _, item := range items {
		if item == "" {
			continue
		}
		if _, exists := counts[item]; !exists {
			order = append(order, item)
		}
		counts[item]++
	}

	var result strings.Builder
	for i, item := range order {
		if i > 0 {
			result.WriteString(separator)
		}
		count := counts[item]
		if count > 1 {
			result.WriteString(fmt.Sprintf("(%s) x %d", item, count))
		} else {
			result.WriteString(item)
		}
	}

	return result.String()
}
