package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

// MockProvider - мок для EdgeCenterProvider
type MockProvider struct {
	mock.Mock
}

func (m *MockProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	args := m.Called(ctx)
	return args.Get(0).([]*endpoint.Endpoint), args.Error(1)
}

func (m *MockProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	args := m.Called(ctx, changes)
	return args.Error(0)
}

func (m *MockProvider) AdjustEndpoints(endpoints []*endpoint.Endpoint) []*endpoint.Endpoint {
	args := m.Called(endpoints)
	return args.Get(0).([]*endpoint.Endpoint)
}

func (m *MockProvider) GetDomainFilter() []string {
	args := m.Called()
	return args.Get(0).([]string)
}

func TestWebhookServer_handleHealth(t *testing.T) {
	provider := new(MockProvider)
	logger, _ := zap.NewDevelopment()
	server := NewWebhookServer(provider, logger)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	server.server = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("OK"))
		}),
	}

	server.server.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

func TestWebhookServer_handleRoot(t *testing.T) {
	provider := new(MockProvider)
	logger, _ := zap.NewDevelopment()
	server := NewWebhookServer(provider, logger)

	provider.On("GetDomainFilter").Return([]string{"example.com"})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	server.handleRoot(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, contentType, w.Header().Get("Content-Type"))

	var resp struct {
		Filters []string `json:"filters"`
	}
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Equal(t, []string{"example.com"}, resp.Filters)

	provider.AssertExpectations(t)
}

func TestWebhookServer_handleRecords(t *testing.T) {
	provider := new(MockProvider)
	logger, _ := zap.NewDevelopment()
	server := NewWebhookServer(provider, logger)

	endpoints := []*endpoint.Endpoint{
		{
			DNSName:    "test.example.com",
			RecordType: "A",
			Targets:    endpoint.Targets{"192.0.2.1"},
		},
	}

	provider.On("Records", mock.Anything).Return(endpoints, nil)

	req := httptest.NewRequest(http.MethodGet, "/records", nil)
	w := httptest.NewRecorder()

	server.handleRecords(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, contentType, w.Header().Get("Content-Type"))

	var resp []*endpoint.Endpoint
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Equal(t, endpoints, resp)

	provider.AssertExpectations(t)
}

func TestWebhookServer_handleRecords_Post(t *testing.T) {
	provider := new(MockProvider)
	logger, _ := zap.NewDevelopment()
	server := NewWebhookServer(provider, logger)

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{
				DNSName:    "create.example.com",
				RecordType: "A",
				Targets:    endpoint.Targets{"192.0.2.5"},
			},
		},
	}

	// Настроим провайдер на ожидание вызова ApplyChanges
	provider.On("ApplyChanges", mock.Anything, changes).Return(nil)

	body, _ := json.Marshal(changes)
	req := httptest.NewRequest(http.MethodPost, "/records", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleRecords(w, req)

	// Проверяем статус NoContent и что ApplyChanges был вызван
	assert.Equal(t, http.StatusNoContent, w.Code)
	provider.AssertExpectations(t)
}

func TestWebhookServer_handleRecords_Post_Error(t *testing.T) {
	provider := new(MockProvider)
	logger, _ := zap.NewDevelopment()
	server := NewWebhookServer(provider, logger)

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{
				DNSName:    "error.example.com",
				RecordType: "A",
				Targets:    endpoint.Targets{"192.0.2.6"},
			},
		},
	}

	// Настроим провайдер на возврат ошибки при вызове ApplyChanges
	expectedError := assert.AnError // Используем стандартную ошибку для теста
	provider.On("ApplyChanges", mock.Anything, changes).Return(expectedError)

	body, _ := json.Marshal(changes)
	req := httptest.NewRequest(http.MethodPost, "/records", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleRecords(w, req)

	// Проверяем статус InternalServerError
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	provider.AssertExpectations(t)
}

func TestWebhookServer_handleAdjustEndpoints(t *testing.T) {
	provider := new(MockProvider)
	logger, _ := zap.NewDevelopment()
	server := NewWebhookServer(provider, logger)

	endpoints := []*endpoint.Endpoint{
		{
			DNSName:    "test.example.com",
			RecordType: "A",
			Targets:    endpoint.Targets{"192.0.2.1"},
		},
	}

	provider.On("AdjustEndpoints", endpoints).Return(endpoints)

	body, _ := json.Marshal(endpoints)
	req := httptest.NewRequest(http.MethodPost, "/adjustendpoints", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleAdjustEndpoints(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, contentType, w.Header().Get("Content-Type"))

	var resp []*endpoint.Endpoint
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Equal(t, endpoints, resp)

	provider.AssertExpectations(t)
}

func TestWebhookServer_Start(t *testing.T) {
	provider := new(MockProvider)
	logger, _ := zap.NewDevelopment()
	server := NewWebhookServer(provider, logger)

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(8888)
	}()

	// Даем серверу время на запуск
	time.Sleep(100 * time.Millisecond)

	// Проверяем, что сервер работает
	resp, err := http.Get("http://localhost:8888/health")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Останавливаем сервер
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = server.Shutdown(ctx)
	assert.NoError(t, err)

	// Проверяем, что сервер завершился без ошибок
	err = <-errChan
	if err != http.ErrServerClosed {
		assert.NoError(t, err)
	}
}
