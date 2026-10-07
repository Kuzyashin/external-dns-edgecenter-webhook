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

func (m *MockClient) RRSet(ctx context.Context, zoneName, recordName, recordType string) (dnssdk.RRSet, error) {
	args := m.Called(ctx, zoneName, recordName, recordType)
	return args.Get(0).(dnssdk.RRSet), args.Error(1)
}

func (m *MockClient) UpdateRRSet(ctx context.Context, zoneName, recordName, recordType string, record dnssdk.RRSet) error {
	args := m.Called(ctx, zoneName, recordName, recordType, record)
	return args.Error(0)
}

// aRecords — набор A-записей без meta, как их возвращает/принимает EdgeCenter.
func aRecords(ips ...string) []dnssdk.ResourceRecord {
	out := make([]dnssdk.ResourceRecord, 0, len(ips))
	for _, ip := range ips {
		out = append(out, dnssdk.ResourceRecord{Content: dnssdk.ToRecordType("A", ip).ToContent(), Enabled: true})
	}
	return out
}

var notFound = dnssdk.APIError{StatusCode: 404}

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

	// В EdgeCenter старый адрес — обновляем на месте (PUT), без удаления
	mockClient.On("RRSet", mock.Anything, "example.com", "update.example.com", "A").
		Return(dnssdk.RRSet{TTL: 600, Records: aRecords("192.0.2.1")}, nil)
	mockClient.On("UpdateRRSet", mock.Anything, "example.com", "update.example.com", "A",
		dnssdk.RRSet{TTL: 600, Records: aRecords("192.0.2.10")}).Return(nil)

	// Действие
	err = provider.ApplyChanges(context.Background(), changes)

	// Проверка
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "DeleteRRSet")
	mockClient.AssertNotCalled(t, "AddZoneRRSet")
}

// Регрессия 07.10.2026: external-dns раз в interval присылает UpdateNew с TTL=0
// (TTL не задан в ingress) при той же цели. Раньше это считалось «изменением»,
// запись удалялась и создавалась заново — при сбое создания домен пропадал.
func TestEdgeCenterProvider_ApplyChanges_UpdateUnsetTTLSameTargetsIsNoop(t *testing.T) {
	mockClient := new(MockClient)
	provider, err := NewEdgeCenterProvider(mockClient, endpoint.NewDomainFilter([]string{"ruptly.video"}), zap.NewNop(), false, 600)
	assert.NoError(t, err)
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{{Name: "ruptly.video"}}, nil)

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{{DNSName: "sentry.ops.ruptly.video.", RecordType: "A", Targets: endpoint.Targets{"135.106.158.220"}, RecordTTL: 600}},
		UpdateNew: []*endpoint.Endpoint{{DNSName: "sentry.ops.ruptly.video.", RecordType: "A", Targets: endpoint.Targets{"135.106.158.220"}}},
	}

	assert.NoError(t, provider.ApplyChanges(context.Background(), changes))
	mockClient.AssertNotCalled(t, "RRSet")
	mockClient.AssertNotCalled(t, "UpdateRRSet")
	mockClient.AssertNotCalled(t, "DeleteRRSet")
	mockClient.AssertNotCalled(t, "AddZoneRRSet")
}

// Даже если external-dns считает запись изменённой, но в EdgeCenter она уже такая,
// как нужно, — API на запись не дёргаем.
func TestEdgeCenterProvider_ApplyChanges_UpdateSkipWhenEdgeCenterMatches(t *testing.T) {
	mockClient := new(MockClient)
	provider, err := NewEdgeCenterProvider(mockClient, endpoint.NewDomainFilter([]string{"example.com"}), zap.NewNop(), false, 600)
	assert.NoError(t, err)
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{{Name: "example.com"}}, nil)

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{{DNSName: "x.example.com.", RecordType: "A", Targets: endpoint.Targets{"10.0.0.9"}, RecordTTL: 300}},
		UpdateNew: []*endpoint.Endpoint{{DNSName: "x.example.com.", RecordType: "A", Targets: endpoint.Targets{"10.0.0.1"}}},
	}
	// фактическое состояние уже 10.0.0.1 (кто-то поправил) — TTL из EdgeCenter сохраняется
	mockClient.On("RRSet", mock.Anything, "example.com", "x.example.com", "A").
		Return(dnssdk.RRSet{TTL: 300, Records: aRecords("10.0.0.1")}, nil)

	assert.NoError(t, provider.ApplyChanges(context.Background(), changes))
	mockClient.AssertNotCalled(t, "UpdateRRSet")
	mockClient.AssertNotCalled(t, "DeleteRRSet")
	mockClient.AssertNotCalled(t, "AddZoneRRSet")
}

// Не заданный TTL при реальном изменении целей сохраняет текущий TTL из EdgeCenter.
func TestEdgeCenterProvider_ApplyChanges_UpdateKeepsCurrentTTLWhenUnset(t *testing.T) {
	mockClient := new(MockClient)
	provider, err := NewEdgeCenterProvider(mockClient, endpoint.NewDomainFilter([]string{"example.com"}), zap.NewNop(), false, 600)
	assert.NoError(t, err)
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{{Name: "example.com"}}, nil)

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{{DNSName: "y.example.com.", RecordType: "A", Targets: endpoint.Targets{"10.0.0.1"}, RecordTTL: 60}},
		UpdateNew: []*endpoint.Endpoint{{DNSName: "y.example.com.", RecordType: "A", Targets: endpoint.Targets{"10.0.0.2"}}},
	}
	mockClient.On("RRSet", mock.Anything, "example.com", "y.example.com", "A").
		Return(dnssdk.RRSet{TTL: 60, Records: aRecords("10.0.0.1")}, nil)
	mockClient.On("UpdateRRSet", mock.Anything, "example.com", "y.example.com", "A",
		dnssdk.RRSet{TTL: 60, Records: aRecords("10.0.0.2")}).Return(nil)

	assert.NoError(t, provider.ApplyChanges(context.Background(), changes))
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "DeleteRRSet")
}

// Записи в EdgeCenter нет — при update создаём её, удаления не делаем.
func TestEdgeCenterProvider_ApplyChanges_UpdateCreatesWhenMissing(t *testing.T) {
	mockClient := new(MockClient)
	provider, err := NewEdgeCenterProvider(mockClient, endpoint.NewDomainFilter([]string{"example.com"}), zap.NewNop(), false, 600)
	assert.NoError(t, err)
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{{Name: "example.com"}}, nil)

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{{DNSName: "gone.example.com.", RecordType: "A", Targets: endpoint.Targets{"10.0.0.1"}, RecordTTL: 300}},
		UpdateNew: []*endpoint.Endpoint{{DNSName: "gone.example.com.", RecordType: "A", Targets: endpoint.Targets{"10.0.0.5"}}},
	}
	mockClient.On("RRSet", mock.Anything, "example.com", "gone.example.com", "A").Return(dnssdk.RRSet{}, notFound)
	mockClient.On("AddZoneRRSet", mock.Anything, "example.com", "gone.example.com", "A", aRecords("10.0.0.5"), 600, mock.Anything).Return(nil)

	assert.NoError(t, provider.ApplyChanges(context.Background(), changes))
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "DeleteRRSet")
	mockClient.AssertNotCalled(t, "UpdateRRSet")
}

// Ошибка API при обновлении не приводит к удалению записи.
func TestEdgeCenterProvider_ApplyChanges_UpdateErrorKeepsRecord(t *testing.T) {
	mockClient := new(MockClient)
	provider, err := NewEdgeCenterProvider(mockClient, endpoint.NewDomainFilter([]string{"example.com"}), zap.NewNop(), false, 600)
	assert.NoError(t, err)
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{{Name: "example.com"}}, nil)

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{{DNSName: "z.example.com.", RecordType: "A", Targets: endpoint.Targets{"10.0.0.1"}, RecordTTL: 300}},
		UpdateNew: []*endpoint.Endpoint{{DNSName: "z.example.com.", RecordType: "A", Targets: endpoint.Targets{"10.0.0.2"}, RecordTTL: 300}},
	}
	mockClient.On("RRSet", mock.Anything, "example.com", "z.example.com", "A").
		Return(dnssdk.RRSet{TTL: 300, Records: aRecords("10.0.0.1")}, nil)
	mockClient.On("UpdateRRSet", mock.Anything, "example.com", "z.example.com", "A", mock.Anything).
		Return(assert.AnError)

	assert.Error(t, provider.ApplyChanges(context.Background(), changes))
	mockClient.AssertNotCalled(t, "DeleteRRSet")
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

	mockClient.On("RRSet", mock.Anything, "example.com", "ttl.example.com", "A").
		Return(dnssdk.RRSet{TTL: 300, Records: aRecords("10.0.0.1")}, nil)
	mockClient.On("UpdateRRSet", mock.Anything, "example.com", "ttl.example.com", "A",
		dnssdk.RRSet{TTL: 600, Records: aRecords("10.0.0.1")}).Return(nil)

	err = provider.ApplyChanges(context.Background(), changes)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "DeleteRRSet")
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

func TestParseGeoDNSConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    endpoint.ProviderSpecific
		expected []GeoRecord
	}{
		{
			name:     "No geodns annotation",
			input:    endpoint.ProviderSpecific{},
			expected: nil,
		},
		{
			name: "Valid geodns with countries",
			input: endpoint.ProviderSpecific{
				{Name: "webhook/edgecenter-geodns", Value: `[{"targets":["168.119.120.9"],"countries":["ae","de"]}]`},
			},
			expected: []GeoRecord{
				{Targets: []string{"168.119.120.9"}, Countries: []string{"ae", "de"}},
			},
		},
		{
			name: "Valid geodns with continents",
			input: endpoint.ProviderSpecific{
				{Name: "webhook/edgecenter-geodns", Value: `[{"targets":["10.0.0.1"],"continents":["AS","EU"]}]`},
			},
			expected: []GeoRecord{
				{Targets: []string{"10.0.0.1"}, Continents: []string{"AS", "EU"}},
			},
		},
		{
			name: "Multiple geo records",
			input: endpoint.ProviderSpecific{
				{Name: "webhook/edgecenter-geodns", Value: `[{"targets":["10.0.0.1"],"countries":["ae"]},{"targets":["10.0.0.2"],"continents":["EU"]}]`},
			},
			expected: []GeoRecord{
				{Targets: []string{"10.0.0.1"}, Countries: []string{"ae"}},
				{Targets: []string{"10.0.0.2"}, Continents: []string{"EU"}},
			},
		},
		{
			name: "Invalid JSON",
			input: endpoint.ProviderSpecific{
				{Name: "webhook/edgecenter-geodns", Value: `not-json`},
			},
			expected: nil,
		},
		{
			name: "Other annotation ignored",
			input: endpoint.ProviderSpecific{
				{Name: "some-other", Value: `[{"targets":["10.0.0.1"]}]`},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseGeoDNSConfig(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildGeoDNSRecords(t *testing.T) {
	logger := zap.NewNop()

	defaultContent := dnssdk.ToRecordType("A", "158.160.226.68")
	defaultRecords := []dnssdk.ResourceRecord{
		{Content: defaultContent.ToContent(), Enabled: true},
	}

	geoConfig := []GeoRecord{
		{Targets: []string{"168.119.120.9"}, Countries: []string{"ae", "de", "nl"}},
	}

	records, filters := buildGeoDNSRecords(defaultRecords, geoConfig, nil, 1, "A", logger)

	// Should have 2 records: default + geo
	assert.Len(t, records, 2)

	// Default record should have default meta
	assert.Equal(t, true, records[0].Meta["default"])

	// Geo record should have countries meta
	assert.Equal(t, []string{"ae", "de", "nl"}, records[1].Meta["countries"])

	// Should have 3 filters: geodns, default, first_n
	assert.Len(t, filters, 3)
	assert.Equal(t, "geodns", filters[0].Type)
	assert.Equal(t, "default", filters[1].Type)
	assert.Equal(t, "first_n", filters[2].Type)
}

func TestBuildGeoDNSRecords_MultipleGeoTargets(t *testing.T) {
	logger := zap.NewNop()

	defaultContent := dnssdk.ToRecordType("A", "10.0.0.1")
	defaultRecords := []dnssdk.ResourceRecord{
		{Content: defaultContent.ToContent(), Enabled: true},
	}

	geoConfig := []GeoRecord{
		{Targets: []string{"10.0.0.2"}, Countries: []string{"ae"}},
		{Targets: []string{"10.0.0.3"}, Continents: []string{"EU"}},
	}

	records, filters := buildGeoDNSRecords(defaultRecords, geoConfig, nil, 1, "A", logger)

	// 1 default + 2 geo = 3
	assert.Len(t, records, 3)
	assert.Equal(t, true, records[0].Meta["default"])
	assert.Equal(t, []string{"ae"}, records[1].Meta["countries"])
	assert.Equal(t, []string{"EU"}, records[2].Meta["continents"])
	assert.Len(t, filters, 3)
}

func TestBuildGeoDNSRecords_Healthcheck(t *testing.T) {
	logger := zap.NewNop()

	defaultContent := dnssdk.ToRecordType("A", "10.0.0.1")
	defaultRecords := []dnssdk.ResourceRecord{
		{Content: defaultContent.ToContent(), Enabled: true},
	}
	geoConfig := []GeoRecord{
		{Targets: []string{"10.0.0.2"}, Countries: []string{"de"}},
	}
	hc := &dnssdk.FailoverMeta{Protocol: "ICMP", Frequency: 10, Timeout: 10}

	records, filters := buildGeoDNSRecords(defaultRecords, geoConfig, hc, 1, "A", logger)

	// default record is marked both default and backup (failover target)
	assert.Equal(t, true, records[0].Meta["default"])
	assert.Equal(t, true, records[0].Meta["backup"])
	// geo record carries the failover healthcheck meta
	assert.Equal(t, hc, records[1].Meta["failover"])
	// is_healthy filter inserted after geodns: geodns, is_healthy, default, first_n
	assert.Len(t, filters, 4)
	assert.Equal(t, "geodns", filters[0].Type)
	assert.Equal(t, "is_healthy", filters[1].Type)
	assert.Equal(t, "default", filters[2].Type)
	assert.Equal(t, "first_n", filters[3].Type)
}

func TestEdgeCenterProvider_ApplyChanges_CreateGeoDNS(t *testing.T) {
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"viory.video"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
	assert.NoError(t, err)

	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{Name: "viory.video"},
	}, nil)

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{
				DNSName:    "stenogram.viory.video.",
				RecordType: "A",
				Targets:    endpoint.Targets{"158.160.226.68"},
				RecordTTL:  endpoint.TTL(60),
				ProviderSpecific: endpoint.ProviderSpecific{
					{Name: "webhook/edgecenter-geodns", Value: `[{"targets":["168.119.120.9"],"countries":["ae","de","nl"]}]`},
				},
			},
		},
	}

	// Expected: default record with meta + geo record with meta
	defaultContent := dnssdk.ToRecordType("A", "158.160.226.68")
	geoContent := dnssdk.ToRecordType("A", "168.119.120.9")
	expectedRecords := []dnssdk.ResourceRecord{
		{Content: defaultContent.ToContent(), Enabled: true, Meta: map[string]interface{}{"default": true}},
		{Content: geoContent.ToContent(), Enabled: true, Meta: map[string]interface{}{"countries": []string{"ae", "de", "nl"}}},
	}

	mockClient.On("AddZoneRRSet", mock.Anything, "viory.video", "stenogram.viory.video", "A", expectedRecords, 60, mock.Anything).Return(nil)

	err = provider.ApplyChanges(context.Background(), changes)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestEdgeCenterProvider_ApplyChanges_UpdateGeoDNS(t *testing.T) {
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"viory.video"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
	assert.NoError(t, err)

	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{Name: "viory.video"},
	}, nil)

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{
			{
				DNSName:    "stenogram.viory.video.",
				RecordType: "A",
				Targets:    endpoint.Targets{"158.160.226.68"},
				RecordTTL:  endpoint.TTL(60),
			},
		},
		UpdateNew: []*endpoint.Endpoint{
			{
				DNSName:    "stenogram.viory.video.",
				RecordType: "A",
				Targets:    endpoint.Targets{"158.160.226.99"},
				RecordTTL:  endpoint.TTL(60),
				ProviderSpecific: endpoint.ProviderSpecific{
					{Name: "webhook/edgecenter-geodns", Value: `[{"targets":["168.119.120.9"],"countries":["ae","de","nl"]}]`},
				},
			},
		},
	}

	mockClient.On("RRSet", mock.Anything, "viory.video", "stenogram.viory.video", "A").
		Return(dnssdk.RRSet{TTL: 60, Records: aRecords("158.160.226.68")}, nil)

	defaultContent := dnssdk.ToRecordType("A", "158.160.226.99")
	geoContent := dnssdk.ToRecordType("A", "168.119.120.9")
	expectedRecords := []dnssdk.ResourceRecord{
		{Content: defaultContent.ToContent(), Enabled: true, Meta: map[string]interface{}{"default": true}},
		{Content: geoContent.ToContent(), Enabled: true, Meta: map[string]interface{}{"countries": []string{"ae", "de", "nl"}}},
	}
	expectedFilters := []dnssdk.RecordFilter{
		dnssdk.NewGeoDNSFilter(0, false),
		dnssdk.NewDefaultFilter(1, false),
		dnssdk.NewFirstNFilter(1, false),
	}

	mockClient.On("UpdateRRSet", mock.Anything, "viory.video", "stenogram.viory.video", "A",
		dnssdk.RRSet{TTL: 60, Records: expectedRecords, Filters: expectedFilters}).Return(nil)

	err = provider.ApplyChanges(context.Background(), changes)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "DeleteRRSet")
}

// GeoDNS-запись, которая в EdgeCenter уже совпадает (meta приходит из JSON как
// []interface{}), повторно не обновляется.
func TestEdgeCenterProvider_ApplyChanges_UpdateGeoDNSUnchangedIsNoop(t *testing.T) {
	mockClient := new(MockClient)
	provider, err := NewEdgeCenterProvider(mockClient, endpoint.NewDomainFilter([]string{"viory.video"}), zap.NewNop(), false, 600)
	assert.NoError(t, err)
	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{{Name: "viory.video"}}, nil)

	geo := endpoint.ProviderSpecific{{Name: "webhook/edgecenter-geodns", Value: `[{"targets":["168.119.120.9"],"countries":["ae","de"]}]`}}
	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{{DNSName: "s.viory.video.", RecordType: "A", Targets: endpoint.Targets{"1.2.3.4"}, RecordTTL: 60}},
		UpdateNew: []*endpoint.Endpoint{{DNSName: "s.viory.video.", RecordType: "A", Targets: endpoint.Targets{"1.2.3.4"}, RecordTTL: 60, ProviderSpecific: geo}},
	}
	current := dnssdk.RRSet{
		TTL: 60,
		Records: []dnssdk.ResourceRecord{
			{Content: []interface{}{"168.119.120.9"}, Enabled: true, Meta: map[string]interface{}{"countries": []interface{}{"ae", "de"}}},
			{Content: []interface{}{"1.2.3.4"}, Enabled: true, Meta: map[string]interface{}{"default": true}},
		},
		Filters: []dnssdk.RecordFilter{dnssdk.NewGeoDNSFilter(0, false), dnssdk.NewDefaultFilter(1, false), dnssdk.NewFirstNFilter(1, false)},
	}
	mockClient.On("RRSet", mock.Anything, "viory.video", "s.viory.video", "A").Return(current, nil)

	assert.NoError(t, provider.ApplyChanges(context.Background(), changes))
	mockClient.AssertNotCalled(t, "UpdateRRSet")
	mockClient.AssertNotCalled(t, "DeleteRRSet")
	mockClient.AssertNotCalled(t, "AddZoneRRSet")
}

func TestEdgeCenterProvider_ApplyChanges_CreateWithoutGeoDNS_Unchanged(t *testing.T) {
	// Verify that records WITHOUT geodns annotation still work as before
	mockClient := new(MockClient)
	logger := zap.NewNop()
	domainFilter := endpoint.NewDomainFilter([]string{"example.com"})
	provider, err := NewEdgeCenterProvider(mockClient, domainFilter, logger, false, 600)
	assert.NoError(t, err)

	mockClient.On("Zones", mock.Anything, mock.Anything).Return([]dnssdk.Zone{
		{Name: "example.com"},
	}, nil)

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			{
				DNSName:    "plain.example.com.",
				RecordType: "A",
				Targets:    endpoint.Targets{"10.0.0.1"},
				RecordTTL:  endpoint.TTL(300),
			},
		},
	}

	// No geo meta on records — plain create
	expectedContent := dnssdk.ToRecordType("A", "10.0.0.1")
	expectedRecords := []dnssdk.ResourceRecord{
		{Content: expectedContent.ToContent(), Enabled: true},
	}

	mockClient.On("AddZoneRRSet", mock.Anything, "example.com", "plain.example.com", "A", expectedRecords, 300, mock.Anything).Return(nil)

	err = provider.ApplyChanges(context.Background(), changes)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}
