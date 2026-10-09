package web

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"wangui/internal/events"
	"wangui/internal/scheduler"
	"wangui/internal/store"
)

// Server bundles the HTTP server, its dependencies and the embedded SPA.
type Server struct {
	Addr      string
	Store     *store.Store
	Sched     *scheduler.Multi
	Bus       *events.Bus
	Logger    *slog.Logger
	SPAFS     fs.FS
	AdminPass string // empty means admin disabled
}

// Run blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(slogRequestLogger(s.Logger))

	h := &handlers{
		store:        s.Store,
		sched:        s.Sched,
		bus:          s.Bus,
		log:          s.Logger,
		adminPass:    s.AdminPass,
		loginLimiter: newRateLimiter(5, time.Minute),
	}

	r.Route("/api/v1", func(r chi.Router) {
		// Public (no auth)
		r.Post("/login", h.login)
		r.Post("/activate", h.activate)
		r.Post("/activate/precheck", h.activatePrecheck)
		r.Post("/rosekhlifa/login", h.adminLogin)
		// User-facing announcements are public-ish: any logged-in user reads
		// them through /me-scoped sessions, but we don't need auth on the
		// list itself — the data is non-sensitive (admin-authored notices).
		r.Get("/announcements", h.listAnnouncements)
		// Lifetime success-sign counter for the user sidebar's brag chip.
		// Single number; no sensitive fields.
		r.Get("/platform-stats", h.platformStats)

		// User endpoints
		r.Group(func(r chi.Router) {
			r.Use(h.userAuth)
			r.Get("/me", h.me)
			r.Put("/token", h.updateToken)
			r.Put("/pin", h.changePin)
			r.Get("/settings", h.getSettings)
			r.Put("/settings", h.updateSettings)
			r.Get("/records", h.records)
			r.Get("/stats", h.stats)
			r.Get("/dorms", h.listDorms)
			r.Post("/sign-now", h.signNow)
			r.Post("/skip-today", h.skipToday)
			r.Post("/notify/test-serverchan", h.testServerChan)
			r.Post("/logout", h.logout)
			r.Delete("/me", h.deleteMe)
		})

		// Admin endpoints (path is obfuscated: /rosekhlifa)
		r.Route("/rosekhlifa", func(r chi.Router) {
			r.Use(h.adminAuth)
			r.Get("/me", h.adminMe)
			r.Post("/logout", h.adminLogout)
			r.Get("/stats", h.adminStats)

			r.Get("/codes", h.adminListCodes)
			r.Post("/codes", h.adminCreateCodes)
			r.Put("/codes/{code}", h.adminUpdateCode)
			r.Delete("/codes/{code}", h.adminDeleteCode)

			r.Get("/users", h.adminListUsers)
			r.Get("/users/{id}", h.adminGetUser)
			r.Put("/users/{id}", h.adminUpdateUser)
			r.Post("/users/{id}/pin", h.adminResetUserPin)
			r.Post("/users/{id}/token", h.adminRefreshUserToken)
			r.Post("/users/{id}/sign-now", h.adminSignNowForUser)
			r.Get("/users/{id}/checkin-status", h.adminCheckinStatusForUser)
			r.Delete("/users/{id}", h.adminDeleteUser)

			r.Get("/dorms", h.adminListDorms)
			r.Post("/dorms", h.adminCreateDorm)
			r.Put("/dorms/{id}", h.adminUpdateDorm)
			r.Delete("/dorms/{id}", h.adminDeleteDorm)
			r.Get("/dorms/{id}/users", h.adminDormUsers)

			r.Get("/guests", h.adminListGuests)
			r.Post("/guests", h.adminCreateGuest)
			r.Put("/guests/{id}", h.adminUpdateGuest)
			r.Delete("/guests/{id}", h.adminDeleteGuest)

			r.Get("/logs", h.adminLogs)
			r.Get("/records.csv", h.adminExportRecordsCSV)
			r.Get("/school-rules", h.adminSchoolRules)
			r.Get("/events", h.adminEvents)

			r.Get("/announcements", h.adminListAnnouncements)
			r.Post("/announcements", h.adminCreateAnnouncement)
			r.Put("/announcements/{id}", h.adminUpdateAnnouncement)
			r.Delete("/announcements/{id}", h.adminDeleteAnnouncement)

			r.Get("/smtp", h.adminGetSMTP)
			r.Put("/smtp", h.adminUpdateSMTP)
			r.Post("/smtp/test", h.adminTestSMTP)
			r.Post("/serverchan/test", h.adminTestServerChan)
		})
	})

	if s.SPAFS != nil {
		mountSPA(r, s.SPAFS)
	}

	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.Logger.Info("http server listening", "addr", s.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func mountSPA(r chi.Router, spa fs.FS) {
	fileServer := http.FileServer(http.FS(spa))
	r.Get("/assets/*", fileServer.ServeHTTP)
	r.Get("/favicon.ico", fileServer.ServeHTTP)
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/") {
			http.NotFound(w, req)
			return
		}
		clean := strings.TrimPrefix(req.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if f, err := spa.Open(clean); err == nil {
			defer f.Close()
			fileServer.ServeHTTP(w, req)
			return
		}
		b, err := fs.ReadFile(spa, "index.html")
		if err != nil {
			http.Error(w, "spa index missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
}

func slogRequestLogger(l *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				return
			}
			l.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"dur_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
