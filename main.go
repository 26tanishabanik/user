package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	corelog "log"

	"github.com/go-kit/kit/log"
	kitprometheus "github.com/go-kit/kit/metrics/prometheus"
	"github.com/microservices-demo/user/api"
	"github.com/microservices-demo/user/db"
	"github.com/microservices-demo/user/db/mongodb"
	stdopentracing "github.com/opentracing/opentracing-go"
	zipkin "github.com/openzipkin/zipkin-go-opentracing"
	stdprometheus "github.com/prometheus/client_golang/prometheus"
	commonMiddleware "github.com/weaveworks/common/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"golang.org/x/net/context"
)

var (
	port string
	zip  string
)

var (
	HTTPLatency = stdprometheus.NewHistogramVec(stdprometheus.HistogramOpts{
		Name:    "request_duration_seconds",
		Help:    "Time (in seconds) spent serving HTTP requests.",
		Buckets: stdprometheus.DefBuckets,
	}, []string{"method", "route", "status_code", "isWS"})
)

const (
	ServiceName = "user"
)

func init() {
	stdprometheus.MustRegister(HTTPLatency)
	flag.StringVar(&zip, "zipkin", os.Getenv("ZIPKIN"), "Zipkin address")
	flag.StringVar(&port, "port", "8084", "Port on which to run")
	db.Register("mongodb", &mongodb.Mongo{})
}

func main() {

	flag.Parse()
	// Mechanical stuff.
	errc := make(chan error)
	ctx := context.Background()

	// Log domain.
	var logger log.Logger
	{
		logger = log.NewLogfmtLogger(os.Stderr)
		logger = log.NewContext(logger).With("ts", log.DefaultTimestampUTC)
		logger = log.NewContext(logger).With("caller", log.DefaultCaller)
	}

	var tracer stdopentracing.Tracer
	{
		if zip == "" {
			tracer = stdopentracing.NoopTracer{}
		} else {
			logger := log.NewContext(logger).With("tracer", "Zipkin")
			logger.Log("addr", zip)
			collector, err := zipkin.NewHTTPCollector(
				zip,
				zipkin.HTTPLogger(logger),
			)
			if err != nil {
				logger.Log("err", err)
				os.Exit(1)
			}
			tracer, err = zipkin.NewTracer(
				zipkin.NewRecorder(collector, false, fmt.Sprintf("localhost:%v", port), ServiceName),
			)
			if err != nil {
				logger.Log("err", err)
				os.Exit(1)
			}
		}
		stdopentracing.InitGlobalTracer(tracer)
	}

	// OpenTelemetry tracing, only when an OTLP endpoint is set.
	otelEnabled := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
	if otelEnabled {
		shutdown, err := initOTel(ctx)
		if err != nil {
			logger.Log("tracer", "OpenTelemetry", "err", err)
			os.Exit(1)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdown(ctx); err != nil {
				logger.Log("tracer", "OpenTelemetry", "err", err)
			}
		}()
	}
	dbconn := false
	for !dbconn {
		err := db.Init()
		if err != nil {
			if err == db.ErrNoDatabaseSelected {
				corelog.Fatal(err)
			}
			corelog.Print(err)
		} else {
			dbconn = true
		}
	}

	fieldKeys := []string{"method"}
	// Service domain.
	var service api.Service
	{
		service = api.NewFixedService()
		service = api.LoggingMiddleware(logger)(service)
		service = api.NewInstrumentingService(
			kitprometheus.NewCounterFrom(
				stdprometheus.CounterOpts{
					Namespace: "microservices_demo",
					Subsystem: "user",
					Name:      "request_count",
					Help:      "Number of requests received.",
				},
				fieldKeys),
			kitprometheus.NewSummaryFrom(stdprometheus.SummaryOpts{
				Namespace: "microservices_demo",
				Subsystem: "user",
				Name:      "request_latency_microseconds",
				Help:      "Total duration of requests in microseconds.",
			}, fieldKeys),
			service,
		)
	}

	// Endpoint domain.
	endpoints := api.MakeEndpoints(service, tracer)

	// HTTP router
	router := api.MakeHTTPHandler(ctx, endpoints, logger, tracer)

	httpMiddleware := []commonMiddleware.Interface{
		commonMiddleware.Instrument{
			Duration:     HTTPLatency,
			RouteMatcher: router,
		},
	}

	// Handler
	handler := commonMiddleware.Merge(httpMiddleware...).Wrap(router)
	if otelEnabled {
		// Routes rename the span to "METHOD /template"; a request that
		// matches no route keeps just the method.
		handler = otelhttp.NewHandler(handler, ServiceName,
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				return r.Method
			}))
	}

	// Create and launch the HTTP server.
	go func() {
		logger.Log("transport", "HTTP", "port", port)
		errc <- http.ListenAndServe(fmt.Sprintf(":%v", port), handler)
	}()

	// Capture interrupts.
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		errc <- fmt.Errorf("%s", <-c)
	}()

	logger.Log("exit", <-errc)
}

// initOTel sets up an OTLP/HTTP trace exporter and W3C propagation. The
// exporter and resource read the standard OTEL_* environment variables.
func initOTel(ctx context.Context) (func(context.Context) error, error) {
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(ServiceName)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		// A malformed OTEL_RESOURCE_ATTRIBUTES; keep the attributes that parsed.
		otel.Handle(err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return tp.Shutdown, nil
}
