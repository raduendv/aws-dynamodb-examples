package http

import (
	stdhttp "net/http"
	"slices"
)

// Method identifies an HTTP method accepted by a route.
type Method string

const (
	GET    Method = "GET"
	POST   Method = "POST"
	PUT    Method = "PUT"
	PATCH  Method = "PATCH"
	DELETE Method = "DELETE"
	HEAD   Method = "HEAD"
)

// CanAcceptMethod validates a request method and writes a 405 response when needed.
func CanAcceptMethod(w stdhttp.ResponseWriter, r *stdhttp.Request, methods ...Method) bool {
	method := Method(r.Method)

	if slices.Contains(methods, method) {
		return true
	}

	w.Header().Set("Allow", string(methods[0]))
	for _, m := range methods[1:] {
		w.Header().Add("Allow", string(m))
	}
	w.WriteHeader(stdhttp.StatusMethodNotAllowed)

	return false
}
