package main

import (
	"context"
	"database/sql"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	_ "github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.19.0"
	"go.opentelemetry.io/otel/trace"
)

type TodoItem struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Completed   bool   `json:"completed"`
}

var (
	db              *sql.DB
	tracer          trace.Tracer
	userStatus      *prometheus.CounterVec
	requestCount    *prometheus.CounterVec
	todoActionCount *prometheus.CounterVec
)

func initMetrics() {
	userStatus = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_request_get_user_status_count",
		Help: "Count of status returned by user",
	}, []string{"user", "status"})

	requestCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_request_count",
		Help: "Total number of requests",
	}, []string{"method", "endpoint"})

	todoActionCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_todo_count",
		Help: "Count of todos",
	}, []string{"action"})

	prometheus.MustRegister(userStatus, requestCount, todoActionCount)
}

func initTracer() trace.Tracer {
	exporter, err := otlptracehttp.New(
		context.Background(),
		otlptracehttp.WithEndpoint("localhost:4318"), // Default OTLP HTTP port
		otlptracehttp.WithInsecure(),                 // Skip TLS for local development
	)
	if err != nil {
		log.Fatalf("failed to create OTLP exporter: %v", err)
	}

	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			semconv.ServiceName("todo-service"),
		),
	)
	if err != nil {
		log.Fatalf("failed to create resource: %v", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Tracer("todo-service")
}

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./test.db")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS todos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		description TEXT,
		completed BOOLEAN DEFAULT 0
	);`)
	if err != nil {
		log.Fatalf("failed to create table: %v", err)
	}
}

func getTodos(c echo.Context) error {
	_, span := tracer.Start(c.Request().Context(), "getTodos")
	defer span.End()

	rows, err := db.Query("SELECT id, title, description, completed FROM todos")
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to query todos")
	}
	defer rows.Close()

	var todos []TodoItem
	for rows.Next() {
		var todo TodoItem
		if err := rows.Scan(&todo.ID, &todo.Title, &todo.Description, &todo.Completed); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to scan row")
		}
		todos = append(todos, todo)
	}

	requestCount.WithLabelValues(http.MethodGet, "/todos").Inc()
	return c.JSON(http.StatusOK, todos)
}

func createTodo(c echo.Context) error {
	_, span := tracer.Start(c.Request().Context(), "createTodo")
	defer span.End()

	var todo TodoItem
	if err := c.Bind(&todo); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	result, err := db.Exec("INSERT INTO todos (title, description, completed) VALUES (?, ?, ?)",
		todo.Title, todo.Description, todo.Completed)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to insert todo")
	}

	id, _ := result.LastInsertId()
	todo.ID = int(id)

	todoActionCount.WithLabelValues("created").Inc()
	requestCount.WithLabelValues(http.MethodPost, "/todos").Inc()
	return c.JSON(http.StatusCreated, todo)
}

func deleteTodo(c echo.Context) error {
	_, span := tracer.Start(c.Request().Context(), "deleteTodo")
	defer span.End()

	id := c.Param("id")
	_, err := db.Exec("DELETE FROM todos WHERE id = ?", id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to delete todo")
	}

	todoActionCount.WithLabelValues("deleted").Inc()
	requestCount.WithLabelValues(http.MethodDelete, "/todos/:id").Inc()
	return c.NoContent(http.StatusNoContent)
}

func metricsHandler(c echo.Context) error {
	promHandler := promhttp.Handler()
	promHandler.ServeHTTP(c.Response(), c.Request())
	return nil
}

func producer() {
	users := []string{"bob", "alice", "jack"}
	for {
		user := users[rand.Intn(len(users))]
		status := "2xx"
		if rand.Float64() > 0.8 {
			status = "4xx"
		}
		userStatus.WithLabelValues(user, status).Inc()
		time.Sleep(2 * time.Second)
	}
}

func main() {
	// Initialize components
	initDB()
	initMetrics()
	tracer = initTracer()

	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	// Routes
	e.GET("/todos", getTodos)
	e.POST("/todos", createTodo)
	e.DELETE("/todos/:id", deleteTodo)
	e.GET("/metrics", metricsHandler)

	// Start background producer
	go producer()

	// Start server
	log.Fatal(e.Start(":8000"))
}
