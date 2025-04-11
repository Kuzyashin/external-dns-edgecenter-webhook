package main

import (
	"context"
	"testing"

	dnssdk "github.com/Edge-Center/edgecenter-dns-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

// MockClient - мок для EdgeCenter DNS клиента
type MockClient struct {
	mock.Mock
}

func (m *MockClient) Zones(ctx context.Context, filters ...func(*dnssdk.ZonesFilter)) ([]dnssdk.Zone, error) {
	args := m.Called(ctx, filters)
	return args.Get(0).([]dnssdk.Zone), args.Error(1)
}

func (m *MockClient) Zone(ctx context.Context, name string) (dnssdk.Zone, error) {
	args := m.Called(ctx, name)
	return args.Get(0).(dnssdk.Zone), args.Error(1)
}

func (m *MockClient) AddZoneRRSet(ctx context.Context, zoneName, recordName, recordType string, records []dnssdk.ResourceRecord, ttl int, opts ...dnssdk.AddZoneOpt) error {
	args := m.Called(ctx, zoneName, recordName, recordType, records, ttl, opts)
	return args.Error(0)
}

func (m *MockClient) DeleteRRSet(ctx context.Context, zoneName, recordName, recordType string) error {
	args := m.Called(ctx, zoneName, recordName, recordType)
	return args.Error(0)
}

func TestNewEdgeCenterProvider(t *testing.T) {
	mockClient := new(MockClient)
	logger, _ := zap.NewDevelopment()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})

	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false)
	assert.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestEdgeCenterProvider_Records(t *testing.T) {
	// Подготовка
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false)
	assert.NoError(t, err)

	// Настройка мока
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{
			Name: "example.com",
		},
	}, nil)

	mockClient.On("Zone", mock.Anything, "example.com").Return(dnssdk.Zone{
		Name: "example.com",
		Records: []dnssdk.ZoneRecord{
			{
				Name:         "test.example.com",
				Type:         "A",
				TTL:          3600,
				ShortAnswers: []string{"192.0.2.1"},
			},
			{
				Name:         "@",
				Type:         "A",
				TTL:          3600,
				ShortAnswers: []string{"192.0.2.2"},
			},
		},
	}, nil)

	// Действие
	records, err := provider.Records(context.Background())

	// Проверка
	assert.NoError(t, err)
	assert.Len(t, records, 2)

	// Проверяем запись поддомена
	assert.Equal(t, "test.example.com.", records[0].DNSName)
	assert.Equal(t, "A", records[0].RecordType)
	assert.Equal(t, endpoint.Targets{"192.0.2.1"}, records[0].Targets)
	assert.Equal(t, endpoint.TTL(3600), records[0].RecordTTL)

	// Проверяем корневую запись зоны
	assert.Equal(t, "example.com.", records[1].DNSName)
	assert.Equal(t, "A", records[1].RecordType)
	assert.Equal(t, endpoint.Targets{"192.0.2.2"}, records[1].Targets)
	assert.Equal(t, endpoint.TTL(3600), records[1].RecordTTL)

	mockClient.AssertExpectations(t)
}

func TestEdgeCenterProvider_ApplyChanges(t *testing.T) {
	// Подготовка
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false)
	assert.NoError(t, err)

	// Настройка мока для Zones
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{
			Name: "example.com",
		},
	}, nil)

	// Создаем тестовые изменения
	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{
				DNSName:    "test.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"192.0.2.1"},
				RecordTTL:  endpoint.TTL(3600),
			},
		},
	}

	// Настройка мока для AddZoneRRSet
	mockClient.On("AddZoneRRSet", mock.Anything, "example.com", "test.example.com", "A", mock.Anything, 3600, mock.Anything).Return(nil)

	// Действие
	err = provider.ApplyChanges(context.Background(), changes)

	// Проверка
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestEdgeCenterProvider_ApplyChanges_DryRun(t *testing.T) {
	// Подготовка
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, true)
	assert.NoError(t, err)

	// Настройка мока для Zones
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{
			Name: "example.com",
		},
	}, nil)

	// Создаем тестовые изменения
	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{
				DNSName:    "test.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"192.0.2.1"},
				RecordTTL:  endpoint.TTL(3600),
			},
		},
	}

	// Действие
	err = provider.ApplyChanges(context.Background(), changes)

	// Проверка
	assert.NoError(t, err)
	// В режиме dry-run не должно быть вызовов AddZoneRRSet
	mockClient.AssertNotCalled(t, "AddZoneRRSet")
}

func TestEdgeCenterProvider_getZoneAndRecordName(t *testing.T) {
	mockClient := new(MockClient)
	logger, _ := zap.NewDevelopment()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})

	provider, _ := NewEdgeCenterProvider(mockClient, domainFilter, logger, false)

	zoneNameMap := map[string]string{
		"example.com": "example.com",
	}

	tests := []struct {
		name           string
		dnsName        string
		expectedZone   string
		expectedRecord string
	}{
		{
			name:           "Valid DNS name",
			dnsName:        "test.example.com.",
			expectedZone:   "example.com",
			expectedRecord: "test.example.com",
		},
		{
			name:           "Root zone record",
			dnsName:        "example.com.",
			expectedZone:   "example.com",
			expectedRecord: "@",
		},
		{
			name:           "Invalid DNS name",
			dnsName:        "test.invalid.com.",
			expectedZone:   "",
			expectedRecord: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zone, record := provider.(*EdgeCenterProvider).getZoneAndRecordName(tt.dnsName, zoneNameMap)
			assert.Equal(t, tt.expectedZone, zone)
			assert.Equal(t, tt.expectedRecord, record)
		})
	}
}
