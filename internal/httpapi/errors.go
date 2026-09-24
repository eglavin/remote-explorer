package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"

	"remote-explorer/internal/fsvc"
	"remote-explorer/internal/safepath"
)

var (
	errUnauthorized      = errors.New("missing or invalid token")
	errOverwriteDisabled = errors.New("overwrite requested but the server was not started with --overwrite")
	errNotMultipart      = errors.New("request is not multipart/form-data")
	errBadMultipart      = errors.New("malformed multipart body")
	errNoFiles           = errors.New("upload contains no files")
	errBadQuery          = errors.New("invalid query parameter")
)

type errorBody struct {
	Error   string   `json:"error"`
	Code    string   `json:"code"`
	Allowed []string `json:"allowed,omitempty"`
}

// classify maps an error to its response. Messages are fixed strings so
// internal details such as absolute paths never reach the client; the full
// error goes to the request log instead.
func classify(err error) (int, errorBody) {
	var tooLarge *http.MaxBytesError
	var extErr *fsvc.ExtNotAllowedError
	switch {
	case errors.Is(err, errUnauthorized):
		return http.StatusUnauthorized, errorBody{Error: "missing or invalid token", Code: "unauthorized"}
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge, errorBody{Error: "upload exceeds the size limit", Code: "too_large"}
	case errors.As(err, &extErr):
		return http.StatusUnsupportedMediaType, errorBody{Error: "extension not allowed", Code: "ext_not_allowed", Allowed: extErr.Allowed}
	case errors.Is(err, safepath.ErrEscape):
		return http.StatusForbidden, errorBody{Error: "path is outside the served folder", Code: "path_escape"}
	case errors.Is(err, fsvc.ErrInvalidName):
		return http.StatusBadRequest, errorBody{Error: "invalid file name", Code: "invalid_filename"}
	case errors.Is(err, safepath.ErrInvalid):
		return http.StatusBadRequest, errorBody{Error: "invalid path", Code: "invalid_path"}
	case errors.Is(err, fsvc.ErrNotFound):
		return http.StatusNotFound, errorBody{Error: "not found", Code: "not_found"}
	case errors.Is(err, fsvc.ErrNotDir):
		return http.StatusBadRequest, errorBody{Error: "not a directory", Code: "not_a_directory"}
	case errors.Is(err, fsvc.ErrIsDir):
		return http.StatusBadRequest, errorBody{Error: "is a directory", Code: "is_directory"}
	case errors.Is(err, fsvc.ErrExists):
		return http.StatusConflict, errorBody{Error: "file already exists", Code: "exists"}
	case errors.Is(err, fsvc.ErrDuplicateName):
		return http.StatusBadRequest, errorBody{Error: "file name repeated in upload", Code: "duplicate_name"}
	case errors.Is(err, errOverwriteDisabled):
		return http.StatusForbidden, errorBody{Error: "overwriting is disabled on this server", Code: "overwrite_disabled"}
	case errors.Is(err, errNotMultipart), errors.Is(err, errBadMultipart):
		return http.StatusBadRequest, errorBody{Error: "expected a multipart/form-data upload", Code: "invalid_upload"}
	case errors.Is(err, errNoFiles):
		return http.StatusBadRequest, errorBody{Error: "upload contains no files", Code: "no_files"}
	case errors.Is(err, errBadQuery):
		return http.StatusBadRequest, errorBody{Error: "invalid query parameter", Code: "invalid_query"}
	case errors.Is(err, fs.ErrPermission):
		return http.StatusForbidden, errorBody{Error: "permission denied", Code: "permission_denied"}
	}
	return http.StatusInternalServerError, errorBody{Error: "internal server error", Code: "internal"}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, body := classify(err)
	if st := stateFrom(r.Context()); st != nil {
		st.errCode, st.err = body.Code, err
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
