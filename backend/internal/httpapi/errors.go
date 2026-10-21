package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/family"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/ledger"
	"github.com/rics69/ai-expense-tracking-bot/backend/internal/user"
)

// writeError maps an error to an HTTP status and the Error schema of the spec.
// Unexpected errors are logged and hidden behind a generic message.
func writeError(log *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	detail := ErrorDetail{Code: ErrCodeInternal, Message: "internal server error"}

	var verr *ledger.ValidationError
	switch {
	case errors.As(err, &verr):
		status = http.StatusBadRequest
		field := verr.Field
		if field == "date" {
			field = "spent_at" // the expenses API calls the date spent_at
		}
		detail = ErrorDetail{Code: ErrCodeValidation, Message: verr.Message, Field: &field}
	case errors.Is(err, ledger.ErrNotFound), errors.Is(err, user.ErrNotFound), errors.Is(err, family.ErrInviteNotFound),
		errors.Is(err, family.ErrNotMember):
		status = http.StatusNotFound
		detail = ErrorDetail{Code: ErrCodeNotFound, Message: err.Error()}
	case errors.Is(err, family.ErrHeadRemoval), errors.Is(err, family.ErrBotUnknown):
		status = http.StatusConflict
		detail = ErrorDetail{Code: ErrCodeConflict, Message: err.Error()}
	case errors.Is(err, ErrNoViewer), errors.Is(err, family.ErrNotHead):
		status = http.StatusForbidden
		detail = ErrorDetail{Code: ErrCodeForbidden, Message: err.Error()}
	case errors.Is(err, ledger.ErrUnknownUser):
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
