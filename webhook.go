package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

// WebhookServer represents the HTTP server for handling webhook requests
type WebhookServer struct {
	provider Provider
	logger   *zap.Logger
	server   *http.Server
}

const contentType = "application/external.dns.webhook+json;version=1"

// NewWebhookServer creates a new webhook server
func NewWebhookServer(provider Provider, logger *zap.Logger) *WebhookServer {
	return &WebhookServer{
		provider: provider,
		logger:   logger,
	}
}

// handleRoot handles the initialization request
func (s *WebhookServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := struct {
		Filters []string `json:"filters"`
	}{
		Filters: s.provider.GetDomainFilter(),
	}

	w.Header().Set("Content-Type", contentType)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("failed to encode response", zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleRecords handles the records request
func (s *WebhookServer) handleRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		records, err := s.provider.Records(r.Context())
		if err != nil {
			s.logger.Error("failed to get records", zap.Error(err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", contentType)
		if err := json.NewEncoder(w).Encode(records); err != nil {
			s.logger.Error("failed to encode response", zap.Error(err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

	case http.MethodPost:
		var req struct {
			Create    []*endpoint.Endpoint `json:"create,omitempty"`
			UpdateOld []*endpoint.Endpoint `json:"updateOld,omitempty"`
			UpdateNew []*endpoint.Endpoint `json:"updateNew,omitempty"`
			Delete    []*endpoint.Endpoint `json:"delete,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.logger.Error("failed to decode request", zap.Error(err))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		changes := &plan.Changes{
			Create:    req.Create,
			UpdateOld: req.UpdateOld,
			UpdateNew: req.UpdateNew,
			Delete:    req.Delete,
		}

		if err := s.provider.ApplyChanges(r.Context(), changes); err != nil {
			s.logger.Error("failed to apply changes", zap.Error(err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAdjustEndpoints handles the adjust endpoints request
func (s *WebhookServer) handleAdjustEndpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var endpoints []*endpoint.Endpoint
	if err := json.NewDecoder(r.Body).Decode(&endpoints); err != nil {
		s.logger.Error("failed to decode request", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	adjustedEndpoints := s.provider.AdjustEndpoints(endpoints)

	w.Header().Set("Content-Type", contentType)
	if err := json.NewEncoder(w).Encode(adjustedEndpoints); err != nil {
		s.logger.Error("failed to encode response", zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// Start starts the webhook server
func (s *WebhookServer) Start(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "OK")
	})
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/records", s.handleRecords)
	mux.HandleFunc("/adjustendpoints", s.handleAdjustEndpoints)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *WebhookServer) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
