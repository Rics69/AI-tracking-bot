package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/expense"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// writeError maps an error to an HTTP status and the Error schema of the spec.
// Unexpected errors are logged and hidden behind a generic message.
func writeError(log *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	detail := ErrorDetail{Code: ErrCodeInternal, Message: "internal server error"}

	var verr *expense.ValidationError
	switch {
	case errors.As(err, &verr):
		status = http.StatusBadRequest
		detail = ErrorDetail{Code: ErrCodeValidation, Message: verr.Message, Field: &verr.Field}
	case errors.Is(err, expense.ErrNotFound), errors.Is(err, user.ErrNotFound), errors.Is(err, family.ErrInviteNotFound):
		status = http.StatusNotFound
		detail = ErrorDetail{Code: ErrCodeNotFound, Message: err.Error()}
	case errors.Is(err, family.ErrHeadRemoval), errors.Is(err, family.ErrBotUnknown):
		status = http.StatusConflict
		detail = ErrorDetail{Code: ErrCodeConflict, Message: err.Error()}
	case errors.Is(err, expense.ErrUnknownUser):
		status = http.StatusBadRequest
		detail = ErrorDetail{Code: ErrCodeUnknownUser, Message: err.Error()}
	default:
		log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeJSON(w, status, Error{Error: detail})
}

// writeBadRequest reports a request the generated code could not parse
// (malformed JSON, a non-numeric id, a bad date in the query, ...).
func writeBadRequest(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, Error{Error: ErrorDetail{Code: ErrCodeBadRequest, Message: err.Error()}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
