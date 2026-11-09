package api

// transport.go contains the binding from endpoints to a concrete transport.
// In our case we just use a REST-y HTTP transport.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/tracing/opentracing"
	httptransport "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
	"github.com/microservices-demo/user/users"
	stdopentracing "github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	ErrInvalidRequest = errors.New("Invalid request")
)

// MakeHTTPHandler mounts the endpoints into a REST-y HTTP handler.
func MakeHTTPHandler(ctx context.Context, e Endpoints, logger log.Logger, tracer stdopentracing.Tracer) *mux.Router {
	r := mux.NewRouter().StrictSlash(false)
	options := []httptransport.ServerOption{
		httptransport.ServerErrorLogger(logger),
		httptransport.ServerErrorEncoder(encodeError),
	}

	// GET /login       Login
	// GET /register    Register
	// GET /health      Health Check

	r.Methods("GET").Path("/login").Handler(httptransport.NewServer(
		ctx,
		e.LoginEndpoint,
		decodeLoginRequest,
		encodeResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "GET /login", logger)))...,
	))
	r.Methods("POST").Path("/register").Handler(httptransport.NewServer(
		ctx,
		e.RegisterEndpoint,
		decodeRegisterRequest,
		encodeResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "POST /register", logger)))...,
	))
	// The {id} routes only give traces a route template. Each sends a request
	// to the same handler the prefix route below would, and is named so the
	// Prometheus route label stays the same.
	userGet := httptransport.NewServer(
		ctx,
		e.UserGetEndpoint,
		decodeGetRequest,
		encodeResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "GET /customers", logger)))...,
	)
	r.Methods("GET").Path("/customers/{id}").Name("customers").Handler(userGet)
	r.Methods("GET").Path("/customers/{id}/addresses").Name("customers").Handler(userGet)
	r.Methods("GET").Path("/customers/{id}/cards").Name("customers").Handler(userGet)
	r.Methods("GET").PathPrefix("/customers").Handler(userGet)
	cardGet := httptransport.NewServer(
		ctx,
		e.CardGetEndpoint,
		decodeGetRequest,
		encodeResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "GET /cards", logger)))...,
	)
	r.Methods("GET").Path("/cards/{id}").Name("cards").Handler(cardGet)
	r.Methods("GET").PathPrefix("/cards").Handler(cardGet)
	addressGet := httptransport.NewServer(
		ctx,
		e.AddressGetEndpoint,
		decodeGetRequest,
		encodeResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "GET /addresses", logger)))...,
	)
	r.Methods("GET").Path("/addresses/{id}").Name("addresses").Handler(addressGet)
	r.Methods("GET").PathPrefix("/addresses").Handler(addressGet)
	r.Methods("POST").Path("/customers").Handler(httptransport.NewServer(
		ctx,
		e.UserPostEndpoint,
		decodeUserRequest,
		encodeResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "POST /customers", logger)))...,
	))
	r.Methods("POST").Path("/addresses").Handler(httptransport.NewServer(
		ctx,
		e.AddressPostEndpoint,
		decodeAddressRequest,
		encodeResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "POST /addresses", logger)))...,
	))
	r.Methods("POST").Path("/cards").Handler(httptransport.NewServer(
		ctx,
		e.CardPostEndpoint,
		decodeCardRequest,
		encodeResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "POST /cards", logger)))...,
	))
	del := httptransport.NewServer(
		ctx,
		e.DeleteEndpoint,
		decodeDeleteRequest,
		encodeResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "DELETE /", logger)))...,
	)
	r.Methods("DELETE").Path("/customers/{id}").Name("root").Handler(del)
	r.Methods("DELETE").Path("/addresses/{id}").Name("root").Handler(del)
	r.Methods("DELETE").Path("/cards/{id}").Name("root").Handler(del)
	r.Methods("DELETE").PathPrefix("/").Handler(del)
	r.Methods("GET").PathPrefix("/health").Handler(httptransport.NewServer(
		ctx,
		e.HealthEndpoint,
		decodeHealthRequest,
		encodeHealthResponse,
		append(options, httptransport.ServerBefore(requestContext, opentracing.FromHTTPRequest(tracer, "GET /health", logger)))...,
	))
	r.Handle("/metrics", promhttp.Handler())
	nameSpansByRoute(r)
	return r
}

// requestContext starts each endpoint from the request's context, which holds
// the OpenTelemetry server span, so MongoDB calls become its children; this
// go-kit's Server would otherwise start from its base context, which is empty
// here. The request's cancellation is dropped, so a client that hangs up does
// not stop MongoDB writes part way, just as before.
func requestContext(_ context.Context, r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
}

// nameSpansByRoute names each request's server span "METHOD /template" and
// sets http.route, so spans never carry the ids in the raw path.
func nameSpansByRoute(r *mux.Router) {
	r.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		tmpl, err := route.GetPathTemplate()
		if err != nil {
			return nil
		}
		next := route.GetHandler()
		route.Handler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			span := trace.SpanFromContext(req.Context())
			span.SetName(req.Method + " " + tmpl)
			span.SetAttributes(semconv.HTTPRoute(tmpl))
			next.ServeHTTP(w, req)
		}))
		return nil
	})
}

func encodeError(_ context.Context, err error, w http.ResponseWriter) {
	code := http.StatusInternalServerError
	switch err {
	case ErrUnauthorized:
		code = http.StatusUnauthorized
	}
	w.WriteHeader(code)
	w.Header().Set("Content-Type", "application/hal+json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":       err.Error(),
		"status_code": code,
		"status_text": http.StatusText(code),
	})
}

func decodeLoginRequest(_ context.Context, r *http.Request) (interface{}, error) {
	u, p, ok := r.BasicAuth()
	if !ok {
		return loginRequest{}, ErrUnauthorized
	}

	return loginRequest{
		Username: u,
		Password: p,
	}, nil
}

func decodeRegisterRequest(_ context.Context, r *http.Request) (interface{}, error) {
	reg := registerRequest{}
	err := json.NewDecoder(r.Body).Decode(&reg)
	if err != nil {
		return nil, err
	}
	return reg, nil
}

func decodeDeleteRequest(_ context.Context, r *http.Request) (interface{}, error) {
	d := deleteRequest{}
	u := strings.Split(r.URL.Path, "/")
	if len(u) == 3 {
		d.Entity = u[1]
		d.ID = u[2]
		return d, nil
	}
	return d, ErrInvalidRequest
}

func decodeGetRequest(_ context.Context, r *http.Request) (interface{}, error) {
	g := GetRequest{}
	u := strings.Split(r.URL.Path, "/")
	if len(u) > 2 {
		g.ID = u[2]
		if len(u) > 3 {
			g.Attr = u[3]
		}
	}
	return g, nil
}

func decodeUserRequest(_ context.Context, r *http.Request) (interface{}, error) {
	defer r.Body.Close()
	u := users.User{}
	err := json.NewDecoder(r.Body).Decode(&u)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func decodeAddressRequest(_ context.Context, r *http.Request) (interface{}, error) {
	defer r.Body.Close()
	a := addressPostRequest{}
	err := json.NewDecoder(r.Body).Decode(&a)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func decodeCardRequest(_ context.Context, r *http.Request) (interface{}, error) {
	defer r.Body.Close()
	c := cardPostRequest{}
	err := json.NewDecoder(r.Body).Decode(&c)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func decodeHealthRequest(_ context.Context, r *http.Request) (interface{}, error) {
	return struct{}{}, nil
}

func encodeHealthResponse(ctx context.Context, w http.ResponseWriter, response interface{}) error {
	return encodeResponse(ctx, w, response.(healthResponse))
}

func encodeResponse(_ context.Context, w http.ResponseWriter, response interface{}) error {
	// All of our response objects are JSON serializable, so we just do that.
	w.Header().Set("Content-Type", "application/hal+json")
	return json.NewEncoder(w).Encode(response)
}
