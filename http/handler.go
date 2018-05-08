package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	lib "github.com/hauxe/gom/library"
	"github.com/pkg/errors"
)

var (
	httpMethods = []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodOptions,
	}
	allowOrigins     = "*"
	allowCredentials = "true"
	allowMethods     = lib.JoinWithComma(httpMethods)
	allowHeaders     = lib.JoinWithComma([]string{
		HeaderContentType,
		HeaderAuthorization,
		HeaderOrigin,
		HeaderAccept,
	})
	exposeHeaders = lib.JoinWithComma([]string{HeaderContentType})
)

// ParamValidator route param validator type
type ParamValidator func(context.Context, interface{}) error

// ServerRoute defines route
type ServerRoute struct {
	Name       string
	Method     string
	Path       string
	Validators []ParamValidator
	Handler    http.HandlerFunc
}

// ServerResponseData defines server response data type, we have 3 type
type ServerResponseData struct {
	Success interface{} `json:"success,omitempty"`
	Error   interface{} `json:"error,omitempty"`
	Others  interface{} `json:"others,omitempty"`
}

// ServerResponse defines generic server response value
type ServerResponse struct {
	ErrorCode    errorCode          `json:"error_code"`
	ErrorMessage string             `json:"error_message"`
	Data         ServerResponseData `json:"data"`
	Time         *lib.TimeRFC3339   `json:"time"`
}

// ParseParameters parses parameters from request body or query
func ParseParameters(r *http.Request, dst interface{}) error {
	var err error
	switch r.Header.Get(HeaderContentType) {
	case ContentTypeForm:
		defer r.Body.Close()
		err = r.ParseForm()
		if err != nil {
			return err
		}
		err = decoder.Decode(dst, r.Form)
	case ContentTypeJSON:
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		// numbers are represented as string instead of float64
		decoder.UseNumber()
		err = decoder.Decode(dst)
	default:
		// parse data from query
		err = decoder.Decode(dst, r.URL.Query())
	}
	if err != nil {
		return err
	}
	// validate parameters
	ctx := r.Context()
	val := ctx.Value(ContextValidatorKey)
	if val == nil {
		return nil
	}
	if validators, ok := val.([]ParamValidator); ok {
		for _, validator := range validators {
			if err = validator(ctx, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

// SendResponse encodes data as JSON object and returns it to client
func SendResponse(w http.ResponseWriter, statusCode int, code errorCode,
	message string, data map[string]interface{}) error {

	w.Header().Set(HeaderContentType, ContentTypeJSON)
	w.WriteHeader(statusCode)
	ti := lib.TimeRFC3339(time.Now())
	respData := ServerResponseData{}
	for k, v := range data {
		switch k {
		case "success":
			respData.Success = v
		case "error":
			respData.Error = v
		default:
			respData.Others = v
		}
	}
	obj := ServerResponse{
		ErrorCode:    code,
		ErrorMessage: message,
		Data:         respData,
		Time:         &ti,
	}
	body, err := json.Marshal(obj)
	if err != nil {
		return errors.Wrap(err, lib.StringTags("send response", "marshal body"))
	}

	_, err = w.Write(body)
	return err
}

func buildRouteHandler(method string, validators []ParamValidator, handle http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// set response headers
		w.Header().Set(HeaderAllowOrigin, allowOrigins)
		w.Header().Set(HeaderAllowCredentials, allowCredentials)
		w.Header().Set(HeaderExposeHeaders, exposeHeaders)

		// preflight request
		if r.Method == http.MethodOptions {
			w.Header().Set(HeaderAllowHeaders, allowHeaders)
			w.Header().Set(HeaderAllowMethods, allowMethods)
			SendResponse(w, http.StatusOK, ErrorCodeSuccess, "ok", nil)
			return
		}

		if r.Method != method {
			SendResponse(w, http.StatusMethodNotAllowed, ErrorCodeMalformedMethod,
				"method is not correct for the requested route", nil)
			return
		}
		ctx := r.Context()
		// inject validator to request context
		if len(validators) > 0 {
			ctx = context.WithValue(ctx, ContextValidatorKey, validators)
		}
		handle(w, r.WithContext(ctx))
	}
}
