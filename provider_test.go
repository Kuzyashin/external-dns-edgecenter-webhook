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

	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
	assert.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestEdgeCenterProvider_Records(t *testing.T) {
	// Подготовка
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
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
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
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

	// Настройка мока для AddZoneRRSet - ожидаем слайс с одной записью
	expectedRecord := dnssdk.ToRecordType("A", "192.0.2.1")
	expectedRecords := []dnssdk.ResourceRecord{{Content: expectedRecord.ToContent(), Enabled: true}}
	mockClient.On("AddZoneRRSet", mock.Anything, "example.com", "test.example.com", "A", expectedRecords, 3600, mock.Anything).Return(nil)

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
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, true, 600)
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

func TestEdgeCenterProvider_ApplyChanges_Update(t *testing.T) {
	// Подготовка
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
	assert.NoError(t, err)

	// Настройка мока для Zones
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{
			Name: "example.com",
		},
	}, nil)

	// Создаем тестовые изменения — targets реально изменились
	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{
			{
				DNSName:    "update.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"192.0.2.1"},
				RecordTTL:  endpoint.TTL(600),
			},
		},
		UpdateNew: []*endpoint.Endpoint{
			{
				DNSName:    "update.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"192.0.2.10"},
				RecordTTL:  endpoint.TTL(600),
			},
		},
	}

	// Настройка мока для DeleteRRSet (для старой записи)
	mockClient.On("DeleteRRSet", mock.Anything, "example.com", "update.example.com", "A").Return(nil)
	// Настройка мока для AddZoneRRSet (для новой записи) - ожидаем слайс с одной записью
	expectedNewRecord := dnssdk.ToRecordType("A", "192.0.2.10")
	expectedNewRecords := []dnssdk.ResourceRecord{{Content: expectedNewRecord.ToContent(), Enabled: true}}
	mockClient.On("AddZoneRRSet", mock.Anything, "example.com", "update.example.com", "A", expectedNewRecords, 600, mock.Anything).Return(nil)

	// Действие
	err = provider.ApplyChanges(context.Background(), changes)

	// Проверка
	assert.NoError(t, err)
	mockClient.AssertExpectations(t) // Проверяем, что оба вызова (Delete и Add) были сделаны
}

func TestEdgeCenterProvider_ApplyChanges_UpdateSkipUnchanged(t *testing.T) {
	// Подготовка
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
	assert.NoError(t, err)

	// Настройка мока для Zones
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{
			Name: "example.com",
		},
	}, nil)

	// UpdateOld и UpdateNew идентичны — ничего не должно вызываться
	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{
			{
				DNSName:    "same.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"192.0.2.1"},
				RecordTTL:  endpoint.TTL(600),
			},
		},
		UpdateNew: []*endpoint.Endpoint{
			{
				DNSName:    "same.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"192.0.2.1"},
				RecordTTL:  endpoint.TTL(600),
			},
		},
	}

	// Действие
	err = provider.ApplyChanges(context.Background(), changes)

	// Проверка — API не дёргался
	assert.NoError(t, err)
	mockClient.AssertNotCalled(t, "DeleteRRSet")
	mockClient.AssertNotCalled(t, "AddZoneRRSet")
}

func TestEdgeCenterProvider_ApplyChanges_UpdateSkipUnchangedMultiTarget(t *testing.T) {
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
	assert.NoError(t, err)

	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{Name: "example.com"},
	}, nil)

	// Те же targets но в разном порядке — должен пропустить
	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{
			{
				DNSName:    "multi.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
				RecordTTL:  endpoint.TTL(300),
			},
		},
		UpdateNew: []*endpoint.Endpoint{
			{
				DNSName:    "multi.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"10.0.0.3", "10.0.0.1", "10.0.0.2"},
				RecordTTL:  endpoint.TTL(300),
			},
		},
	}

	err = provider.ApplyChanges(context.Background(), changes)
	assert.NoError(t, err)
	mockClient.AssertNotCalled(t, "DeleteRRSet")
	mockClient.AssertNotCalled(t, "AddZoneRRSet")
}

func TestEdgeCenterProvider_ApplyChanges_UpdateOnTTLChange(t *testing.T) {
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
	assert.NoError(t, err)

	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{Name: "example.com"},
	}, nil)

	// Те же targets, но TTL изменился — должен обновить
	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{
			{
				DNSName:    "ttl.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"10.0.0.1"},
				RecordTTL:  endpoint.TTL(300),
			},
		},
		UpdateNew: []*endpoint.Endpoint{
			{
				DNSName:    "ttl.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"10.0.0.1"},
				RecordTTL:  endpoint.TTL(600),
			},
		},
	}

	mockClient.On("DeleteRRSet", mock.Anything, "example.com", "ttl.example.com", "A").Return(nil)
	expectedRecord := dnssdk.ToRecordType("A", "10.0.0.1")
	expectedRecords := []dnssdk.ResourceRecord{{Content: expectedRecord.ToContent(), Enabled: true}}
	mockClient.On("AddZoneRRSet", mock.Anything, "example.com", "ttl.example.com", "A", expectedRecords, 600, mock.Anything).Return(nil)

	err = provider.ApplyChanges(context.Background(), changes)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestEdgeCenterProvider_ApplyChanges_Delete(t *testing.T) {
	// Подготовка
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
	assert.NoError(t, err)

	// Настройка мока для Zones
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{
			Name: "example.com",
		},
	}, nil)

	// Создаем тестовые изменения
	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{
			{
				DNSName:    "delete.example.com.",
				RecordType: "A",
			},
		},
	}

	// Настройка мока для DeleteRRSet
	mockClient.On("DeleteRRSet", mock.Anything, "example.com", "delete.example.com", "A").Return(nil)

	// Действие
	err = provider.ApplyChanges(context.Background(), changes)

	// Проверка
	assert.NoError(t, err)
	mockClient.AssertExpectations(t) // Проверяем, что Delete был вызван
}

func TestEdgeCenterProvider_getZoneAndRecordName(t *testing.T) {
	mockClient := new(MockClient)
	logger, _ := zap.NewDevelopment()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com", "test.example.com"})

	provider, _ := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)

	zoneNameMap := map[string]string{
		"example.com":      "example.com",
		"test.example.com": "test.example.com",
	}

	tests := []struct {
		name           string
		dnsName        string
		expectedZone   string
		expectedRecord string
	}{
		{
			name:           "Regular subdomain",
			dnsName:        "sub.example.com.",
			expectedZone:   "example.com",
			expectedRecord: "sub.example.com",
		},
		{
			name:           "Root zone record",
			dnsName:        "example.com.",
			expectedZone:   "example.com",
			expectedRecord: "example.com",
		},
		{
			name:           "Invalid zone",
			dnsName:        "test.invalid.com.",
			expectedZone:   "",
			expectedRecord: "",
		},
		{
			name:           "Nested zone record",
			dnsName:        "sub.test.example.com.",
			expectedZone:   "test.example.com",
			expectedRecord: "sub.test.example.com",
		},
		{
			name:           "Nested zone root record",
			dnsName:        "test.example.com.",
			expectedZone:   "test.example.com",
			expectedRecord: "test.example.com",
		},
		{
			name:           "ExternalDNS A-record in root zone",
			dnsName:        "a-example.com.",
			expectedZone:   "example.com",
			expectedRecord: "a-example.com",
		},
		{
			name:           "ExternalDNS A-record in nested zone",
			dnsName:        "a-test.example.com.",
			expectedZone:   "example.com",
			expectedRecord: "a-test.example.com",
		},
		{
			name:           "ExternalDNS A-record for nested zone record",
			dnsName:        "a-sub.test.example.com.",
			expectedZone:   "test.example.com",
			expectedRecord: "a-sub.test.example.com",
		},
		{
			name:           "TXT record managing nested zone itself (should use parent)",
			dnsName:        "txt-test.example.com.",
			expectedZone:   "example.com", // Expects the parent zone
			expectedRecord: "txt-test.example.com",
		},
		{
			name:           "TXT record managing root zone itself (no parent in map - fallback to longest)",
			dnsName:        "txt-example.com.",
			expectedZone:   "example.com", // Fallback to longest match
			expectedRecord: "txt-example.com",
		},
		{
			name:           "Regular TXT record in nested zone",
			dnsName:        "myrecord.test.example.com.",
			expectedZone:   "test.example.com", // Should use the nested zone
			expectedRecord: "myrecord.test.example.com",
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
