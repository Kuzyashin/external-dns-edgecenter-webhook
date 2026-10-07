package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	dnssdk "github.com/Edge-Center/edgecenter-dns-sdk-go"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

const geoDNSAnnotation = "webhook/edgecenter-geodns"
const healthcheckAnnotation = "webhook/edgecenter-healthcheck"
const maxRecordsAnnotation = "webhook/edgecenter-max-records"

// GeoRecord describes a geo-targeted DNS record from the edgecenter-geodns annotation.
type GeoRecord struct {
	Targets    []string `json:"targets"`
	Continents []string `json:"continents,omitempty"`
	Countries  []string `json:"countries,omitempty"`
}

// DNSClient определяет интерфейс для клиента DNS
type DNSClient interface {
	Zones(ctx context.Context, filters ...func(*dnssdk.ZonesFilter)) ([]dnssdk.Zone, error)
	Zone(ctx context.Context, name string) (dnssdk.Zone, error)
	AddZoneRRSet(ctx context.Context, zoneName, recordName, recordType string, records []dnssdk.ResourceRecord, ttl int, opts ...dnssdk.AddZoneOpt) error
	DeleteRRSet(ctx context.Context, zoneName, recordName, recordType string) error
	RRSet(ctx context.Context, zoneName, recordName, recordType string) (dnssdk.RRSet, error)
	UpdateRRSet(ctx context.Context, zoneName, recordName, recordType string, record dnssdk.RRSet) error
}

// Provider определяет интерфейс для DNS провайдера
type Provider interface {
	Records(ctx context.Context) ([]*endpoint.Endpoint, error)
	ApplyChanges(ctx context.Context, changes *plan.Changes) error
	AdjustEndpoints(endpoints []*endpoint.Endpoint) []*endpoint.Endpoint
	GetDomainFilter() []string
}

// endpointsEqual проверяет, совпадают ли targets, TTL и provider-specific
// аннотации старого (a) и нового (b) endpoint'а. Если всё идентично —
// обновление не требуется. TTL, не заданный в новом endpoint'е (0), считается
// совпадающим с любым: «не задан» значит «оставить текущий», а не «поменять».
func endpointsEqual(a, b *endpoint.Endpoint) bool {
	if b.RecordTTL != 0 && a.RecordTTL != b.RecordTTL {
		return false
	}
	if !providerSpecificEqual(a.ProviderSpecific, b.ProviderSpecific) {
		return false
	}
	if len(a.Targets) != len(b.Targets) {
		return false
	}
	aSorted := make([]string, len(a.Targets))
	bSorted := make([]string, len(b.Targets))
	copy(aSorted, a.Targets)
	copy(bSorted, b.Targets)
	sort.Strings(aSorted)
	sort.Strings(bSorted)
	for i := range aSorted {
		if aSorted[i] != bSorted[i] {
			return false
		}
	}
	return true
}

// providerSpecificEqual сравнивает наши webhook/* аннотации (GeoDNS, healthcheck,
// max-records). Остальные provider-specific свойства на запись не влияют.
func providerSpecificEqual(a, b endpoint.ProviderSpecific) bool {
	pick := func(ps endpoint.ProviderSpecific) map[string]string {
		m := map[string]string{}
		for _, p := range ps {
			if strings.HasPrefix(p.Name, "webhook/") {
				m[p.Name] = p.Value
			}
		}
		return m
	}
	am, bm := pick(a), pick(b)
	if len(am) != len(bm) {
		return false
	}
	for k, v := range am {
		if bm[k] != v {
			return false
		}
	}
	return true
}

// isNotFound — ответ API EdgeCenter 404 (записи нет).
func isNotFound(err error) bool {
	apiErr := new(dnssdk.APIError)
	return errors.As(err, apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// recordKey — нормализованное представление записи для сравнения:
// содержимое, enabled и meta (через JSON, ключи map сортируются).
func recordKey(r dnssdk.ResourceRecord) string {
	meta := "{}"
	if len(r.Meta) > 0 {
		var norm interface{}
		if b, err := json.Marshal(r.Meta); err == nil && json.Unmarshal(b, &norm) == nil {
			if nb, err := json.Marshal(norm); err == nil {
				meta = string(nb)
			}
		}
	}
	return fmt.Sprintf("%s|%t|%s", r.ContentToString(), r.Enabled, meta)
}

// rrsetDiff возвращает причину расхождения текущего RRSet в EdgeCenter с желаемым
// или "" если они совпадают (тогда запись трогать не нужно).
func rrsetDiff(current, desired dnssdk.RRSet) string {
	if current.TTL != desired.TTL {
		return fmt.Sprintf("ttl %d -> %d", current.TTL, desired.TTL)
	}
	if len(current.Filters) != len(desired.Filters) {
		return fmt.Sprintf("filters count %d -> %d", len(current.Filters), len(desired.Filters))
	}
	for i := range current.Filters {
		c, d := current.Filters[i], desired.Filters[i]
		if c.Type != d.Type || c.Limit != d.Limit || c.Strict != d.Strict {
			return fmt.Sprintf("filter #%d %s/%d -> %s/%d", i, c.Type, c.Limit, d.Type, d.Limit)
		}
	}
	keys := func(rs []dnssdk.ResourceRecord) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			out = append(out, recordKey(r))
		}
		sort.Strings(out)
		return out
	}
	ck, dk := keys(current.Records), keys(desired.Records)
	if len(ck) != len(dk) {
		return fmt.Sprintf("records count %d -> %d", len(ck), len(dk))
	}
	for i := range ck {
		if ck[i] != dk[i] {
			return fmt.Sprintf("record %q -> %q", ck[i], dk[i])
		}
	}
	return ""
}

// parseGeoDNSConfig extracts geo DNS records from endpoint ProviderSpecific annotations.
func parseGeoDNSConfig(providerSpecific endpoint.ProviderSpecific) []GeoRecord {
	for _, ps := range providerSpecific {
		if ps.Name == geoDNSAnnotation {
			var records []GeoRecord
			if err := json.Unmarshal([]byte(ps.Value), &records); err != nil {
				return nil
			}
			return records
		}
	}
	return nil
}

// parseHealthcheckConfig extracts the DNS-failover healthcheck from the
// edgecenter-healthcheck annotation. When present, geo (relay) records get a
// `failover` meta so EdgeCenter actively monitors them, the default record is
// marked `backup` (returned only when all monitored records are down), and the
// is_healthy filter is added — giving GeoDNS automatic failover to the main IP.
// Example annotation value: {"protocol":"ICMP","frequency":10,"timeout":10}
func parseHealthcheckConfig(providerSpecific endpoint.ProviderSpecific) *dnssdk.FailoverMeta {
	for _, ps := range providerSpecific {
		if ps.Name == healthcheckAnnotation {
			var hc dnssdk.FailoverMeta
			if err := json.Unmarshal([]byte(ps.Value), &hc); err != nil {
				return nil
			}
			if hc.Protocol == "" {
				return nil
			}
			return &hc
		}
	}
	return nil
}

// parseMaxRecords reads the edgecenter-max-records annotation — how many records
// the first_n filter returns per DNS response. Default 1 (single answer). Set >1
// for client-side redundancy (e.g. 2 → client gets both regional relays and
// retries the other if one is dead). Returns 1 when unset/invalid.
func parseMaxRecords(providerSpecific endpoint.ProviderSpecific) uint {
	for _, ps := range providerSpecific {
		if ps.Name == maxRecordsAnnotation {
			if n, err := strconv.ParseUint(ps.Value, 10, 32); err == nil && n > 0 {
				return uint(n)
			}
		}
	}
	return 1
}

// buildGeoDNSRecords creates resource records with geo metadata and the default record.
// It returns the combined records list and the filters to apply.
func buildGeoDNSRecords(defaultRecords []dnssdk.ResourceRecord, geoConfig []GeoRecord, healthcheck *dnssdk.FailoverMeta, maxRecords uint, recordType string, logger *zap.Logger) ([]dnssdk.ResourceRecord, []dnssdk.RecordFilter) {
	var allRecords []dnssdk.ResourceRecord

	// Mark default records with default meta
	for i := range defaultRecords {
		if defaultRecords[i].Meta == nil {
			defaultRecords[i].Meta = map[string]interface{}{}
		}
		defaultRecords[i].Meta["default"] = true
		// With healthcheck on, the default record becomes the failover target:
		// is_healthy returns backup records only when all non-backup are down.
		if healthcheck != nil {
			defaultRecords[i].Meta["backup"] = true
		}
		allRecords = append(allRecords, defaultRecords[i])
	}

	// Add geo-targeted records
	for _, geo := range geoConfig {
		for _, target := range geo.Targets {
			content := dnssdk.ToRecordType(recordType, target)
			if content == nil {
				logger.Error("Failed to convert geo target to record content",
					zap.String("type", recordType),
					zap.String("target", target))
				continue
			}
			meta := map[string]interface{}{}
			if len(geo.Continents) > 0 {
				meta["continents"] = geo.Continents
			}
			if len(geo.Countries) > 0 {
				meta["countries"] = geo.Countries
			}
			if healthcheck != nil {
				meta["failover"] = healthcheck
			}
			allRecords = append(allRecords, dnssdk.ResourceRecord{
				Content: content.ToContent(),
				Enabled: true,
				Meta:    meta,
			})
		}
	}

	filters := []dnssdk.RecordFilter{
		dnssdk.NewGeoDNSFilter(0, false),
	}
	// is_healthy must run after geodns selects the region's records, so dead
	// relays are dropped and (via backup meta) traffic falls back to the main IP.
	if healthcheck != nil {
		filters = append(filters, dnssdk.NewIsHealthyFilter(0, false))
	}
	filters = append(filters,
		dnssdk.NewDefaultFilter(1, false),
		dnssdk.NewFirstNFilter(maxRecords, false),
	)

	return allRecords, filters
}

// EdgeCenterProvider implements the ExternalDNS provider interface for EdgeCenter DNS
type EdgeCenterProvider struct {
	client       DNSClient
	domainFilter endpoint.DomainFilter
	logger       *zap.Logger
	dryRun       bool
	defaultTTL   int
}

// NewEdgeCenterProvider creates a new EdgeCenter DNS provider
func NewEdgeCenterProvider(client DNSClient, domainFilter endpoint.DomainFilter, logger *zap.Logger, dryRun bool, defaultTTL int) (Provider, error) {
	return &EdgeCenterProvider{
		client:       client,
		domainFilter: domainFilter,
		logger:       logger,
		dryRun:       dryRun,
		defaultTTL:   defaultTTL,
	}, nil
}

// GetDomainFilter returns the list of domain filters
func (p *EdgeCenterProvider) GetDomainFilter() []string {
	return p.domainFilter.Filters
}

// Records returns the list of records in all relevant zones
func (p *EdgeCenterProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	p.logger.Info("Fetching DNS records from EdgeCenter")
	// Apply server-side filtering based on domainFilter
	zones, err := p.client.Zones(ctx, func(filter *dnssdk.ZonesFilter) {
		if len(p.domainFilter.Filters) > 0 {
			filter.Names = p.domainFilter.Filters
			p.logger.Info("Applying server-side zone filter", zap.Strings("filters", p.domainFilter.Filters))
		}
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get zones: %w", err)
	}
	p.logger.Info("Retrieved zones from EdgeCenter", zap.Int("count", len(zones)))

	endpoints := []*endpoint.Endpoint{}
	zoneNameMap := make(map[string]string)

	for _, zone := range zones {
		// No need for client-side filtering anymore
		// if !p.domainFilter.Match(zone.Name) {
		// 	p.logger.Info("Skipping zone - not in domain filter", zap.String("zone", zone.Name))
		// 	continue
		// }
		p.logger.Info("Processing zone", zap.String("zone", zone.Name))
		zoneNameMap[zone.Name] = zone.Name

		zoneDetails, err := p.client.Zone(ctx, zone.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get zone %s details: %w", zone.Name, err)
		}
		p.logger.Info("Retrieved zone details",
			zap.String("zone", zone.Name),
			zap.Int("recordCount", len(zoneDetails.Records)))

		for _, record := range zoneDetails.Records {
			if record.Type == "SOA" || record.Type == "NS" {
				p.logger.Info("Skipping system record",
					zap.String("zone", zone.Name),
					zap.String("type", record.Type),
					zap.String("name", record.Name))
				continue
			}

			name := record.Name
			if name == "@" {
				name = zone.Name
				p.logger.Info("Converting @ record to zone name",
					zap.String("zone", zone.Name),
					zap.String("fullName", name))
			}
			if !strings.HasSuffix(name, ".") {
				name = name + "."
			}

			ep := endpoint.NewEndpoint(
				name,
				record.Type,
				record.ShortAnswers...,
			)
			ep.RecordTTL = endpoint.TTL(record.TTL)
			ep.DNSName = name
			p.logger.Info("Created endpoint",
				zap.String("zone", zone.Name),
				zap.String("record", record.Name),
				zap.String("fullName", name),
				zap.String("type", record.Type),
				zap.Strings("answers", record.ShortAnswers),
				zap.Int("ttl", int(record.TTL)))
			endpoints = append(endpoints, ep)
		}
	}

	p.logger.Info("Finished processing records", zap.Int("totalEndpoints", len(endpoints)))
	return endpoints, nil
}

// ApplyChanges applies the given changes to the EdgeCenter DNS
func (p *EdgeCenterProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	p.logger.Info("Starting to apply DNS changes",
		zap.Int("createCount", len(changes.Create)),
		zap.Int("updateCount", len(changes.UpdateNew)),
		zap.Int("deleteCount", len(changes.Delete)),
		zap.Bool("dryRun", p.dryRun))

	zones, err := p.client.Zones(ctx, func(filter *dnssdk.ZonesFilter) {
		filter.Names = p.domainFilter.Filters
	})
	if err != nil {
		return fmt.Errorf("failed to list zones: %v", err)
	}
	p.logger.Info("Retrieved zones for changes", zap.Int("count", len(zones)))

	zoneNameMap := make(map[string]string)
	for _, z := range zones {
		zoneNameMap[z.Name] = z.Name
		p.logger.Info("Found zone", zap.String("zone", z.Name))
	}

	// Обработка создания записей
	for _, change := range changes.Create {
		p.logger.Info("Processing create request",
			zap.String("dnsName", change.DNSName),
			zap.String("type", change.RecordType),
			zap.Strings("targets", change.Targets))

		zoneName, recordName := p.getZoneAndRecordName(change.DNSName, zoneNameMap)
		if zoneName == "" {
			p.logger.Warn("no matching zone found for record",
				zap.String("record", change.DNSName),
				zap.String("type", change.RecordType))
			continue
		}

		ttl := int(change.RecordTTL)
		if ttl == 0 {
			ttl = p.defaultTTL // Use configured default TTL
			p.logger.Info("Using configured default TTL", zap.Int("ttl", ttl))
		}

		// Prepare resource records from all targets
		resourceRecords := make([]dnssdk.ResourceRecord, 0, len(change.Targets))
		for _, target := range change.Targets {
			content := dnssdk.ToRecordType(change.RecordType, target)
			if content == nil { // Handle potential nil from ToRecordType if type/target combo is invalid
				p.logger.Error("Failed to convert target to record content",
					zap.String("type", change.RecordType),
					zap.String("target", target))
				// Depending on desired behavior, we might skip this target or the whole RRset
				// For now, let's skip the target
				continue
			}
			resourceRecords = append(resourceRecords, dnssdk.ResourceRecord{
				Content: content.ToContent(),
				Enabled: true,
			})
		}

		if len(resourceRecords) == 0 {
			p.logger.Warn("No valid resource records could be created for endpoint",
				zap.String("dnsName", change.DNSName),
				zap.String("type", change.RecordType))
			continue
		}

		if p.dryRun {
			p.logger.Info("Would create record (dry-run)",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Any("records", resourceRecords),
				zap.Int("ttl", ttl))
			continue
		}

		// Check for GeoDNS configuration (only for A/AAAA records)
		geoConfig := parseGeoDNSConfig(change.ProviderSpecific)
		var opts []dnssdk.AddZoneOpt
		if geoConfig != nil && (change.RecordType == "A" || change.RecordType == "AAAA") {
			healthcheck := parseHealthcheckConfig(change.ProviderSpecific)
			maxRecords := parseMaxRecords(change.ProviderSpecific)
			resourceRecords, filters := buildGeoDNSRecords(resourceRecords, geoConfig, healthcheck, maxRecords, change.RecordType, p.logger)
			opts = append(opts, dnssdk.WithFilters(filters...))
			p.logger.Info("Creating GeoDNS record",
				zap.String("original_name", change.DNSName),
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Any("records", resourceRecords),
				zap.Any("filters", filters),
				zap.Int("ttl", ttl))
			err := p.client.AddZoneRRSet(ctx, zoneName, recordName, change.RecordType, resourceRecords, ttl, opts...)
			if err != nil {
				return fmt.Errorf("failed to create geodns record %s: %v", change.DNSName, err)
			}
		} else {
			p.logger.Info("Creating DNS record",
				zap.String("original_name", change.DNSName),
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Any("records", resourceRecords),
				zap.Int("ttl", ttl))
			err := p.client.AddZoneRRSet(ctx, zoneName, recordName, change.RecordType, resourceRecords, ttl)
			if err != nil {
				return fmt.Errorf("failed to create record %s: %v", change.DNSName, err)
			}
		}
		p.logger.Info("Successfully created DNS record",
			zap.String("zone", zoneName),
			zap.String("record", recordName))
	}

	// Обработка обновления записей
	for i, change := range changes.UpdateNew {
		p.logger.Info("Processing update request",
			zap.String("dnsName", change.DNSName),
			zap.String("type", change.RecordType),
			zap.Strings("targets", change.Targets))

		// Сравниваем со старой записью — если targets и TTL не изменились, пропускаем
		if i < len(changes.UpdateOld) {
			old := changes.UpdateOld[i]
			if endpointsEqual(old, change) {
				p.logger.Info("Skipping update, record unchanged",
					zap.String("dnsName", change.DNSName),
					zap.String("type", change.RecordType))
				continue
			}
		}

		zoneName, recordName := p.getZoneAndRecordName(change.DNSName, zoneNameMap)
		if zoneName == "" {
			p.logger.Warn("no matching zone found for record",
				zap.String("record", change.DNSName),
				zap.String("type", change.RecordType))
			continue
		}

		// Prepare resource records from all new targets
		newResourceRecords := make([]dnssdk.ResourceRecord, 0, len(change.Targets))
		for _, target := range change.Targets {
			content := dnssdk.ToRecordType(change.RecordType, target)
			if content == nil {
				p.logger.Error("Failed to convert target to record content for update",
					zap.String("type", change.RecordType),
					zap.String("target", target))
				continue
			}
			newResourceRecords = append(newResourceRecords, dnssdk.ResourceRecord{
				Content: content.ToContent(),
				Enabled: true,
			})
		}

		if len(newResourceRecords) == 0 {
			// Пустой набор целей не повод удалять запись: удаление — только через changes.Delete.
			p.logger.Warn("No valid new resource records could be created for endpoint update, leaving record as is",
				zap.String("dnsName", change.DNSName),
				zap.String("type", change.RecordType))
			continue
		}

		// GeoDNS (только A/AAAA): записи по регионам + фильтры
		var filters []dnssdk.RecordFilter
		if geoConfig := parseGeoDNSConfig(change.ProviderSpecific); geoConfig != nil && (change.RecordType == "A" || change.RecordType == "AAAA") {
			healthcheck := parseHealthcheckConfig(change.ProviderSpecific)
			maxRecords := parseMaxRecords(change.ProviderSpecific)
			newResourceRecords, filters = buildGeoDNSRecords(newResourceRecords, geoConfig, healthcheck, maxRecords, change.RecordType, p.logger)
		}

		// Текущее состояние в EdgeCenter — эталон для сравнения.
		current, err := p.client.RRSet(ctx, zoneName, recordName, change.RecordType)
		exists := err == nil && len(current.Records) > 0
		if err != nil && !isNotFound(err) {
			return fmt.Errorf("failed to read current record for update %s: %v", change.DNSName, err)
		}

		// TTL: если в endpoint'е не задан — оставляем текущий, а не сбрасываем в default.
		ttl := int(change.RecordTTL)
		if ttl == 0 {
			if exists {
				ttl = current.TTL
			} else {
				ttl = p.defaultTTL
			}
		}

		desired := dnssdk.RRSet{TTL: ttl, Records: newResourceRecords, Filters: filters}
		if exists {
			desired.Meta = current.Meta // RRSet-level meta не управляем — не затираем
			if diff := rrsetDiff(current, desired); diff == "" {
				p.logger.Info("Skipping update, record in EdgeCenter already matches",
					zap.String("dnsName", change.DNSName),
					zap.String("type", change.RecordType))
				continue
			} else {
				p.logger.Info("Record differs from desired",
					zap.String("dnsName", change.DNSName),
					zap.String("type", change.RecordType),
					zap.String("diff", diff))
			}
		}

		if p.dryRun {
			p.logger.Info("Would update record (dry-run)",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Bool("exists", exists),
				zap.Any("newRecords", newResourceRecords),
				zap.Int("ttl", ttl))
			continue
		}

		if exists {
			// Атомарная замена набора (PUT): записи ни в какой момент нет «пустой».
			p.logger.Info("Updating DNS record in place",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Any("records", newResourceRecords),
				zap.Any("filters", filters),
				zap.Int("ttl", ttl))
			if err := p.client.UpdateRRSet(ctx, zoneName, recordName, change.RecordType, desired); err != nil {
				return fmt.Errorf("failed to update record %s: %v", change.DNSName, err)
			}
		} else {
			// Записи нет (удалили руками и т.п.) — создаём.
			p.logger.Info("Record missing in EdgeCenter, creating for update",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Int("ttl", ttl))
			if err := p.client.AddZoneRRSet(ctx, zoneName, recordName, change.RecordType, newResourceRecords, ttl, dnssdk.WithFilters(filters...)); err != nil {
				return fmt.Errorf("failed to create record during update %s: %v", change.DNSName, err)
			}
		}
		p.logger.Info("Successfully updated DNS record",
			zap.String("zone", zoneName),
			zap.String("record", recordName))
	}

	// Обработка удаления записей
	for _, change := range changes.Delete {
		p.logger.Info("Processing delete request",
			zap.String("dnsName", change.DNSName),
			zap.String("type", change.RecordType))

		zoneName, recordName := p.getZoneAndRecordName(change.DNSName, zoneNameMap)
		if zoneName == "" {
			p.logger.Warn("no matching zone found for record to delete",
				zap.String("record", change.DNSName),
				zap.String("type", change.RecordType))
			continue
		}

		if p.dryRun {
			p.logger.Info("Would delete record (dry-run)",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType))
			continue
		}

		p.logger.Info("Deleting DNS record",
			zap.String("zone", zoneName),
			zap.String("record", recordName),
			zap.String("type", change.RecordType))

		err := p.client.DeleteRRSet(ctx, zoneName, recordName, change.RecordType)
		if err != nil {
			return fmt.Errorf("failed to delete record %s: %v", change.DNSName, err)
		}
		p.logger.Info("Successfully deleted DNS record",
			zap.String("zone", zoneName),
			zap.String("record", recordName))
	}

	p.logger.Info("Finished applying DNS changes")
	return nil
}

// AdjustEndpoints adjusts the endpoints for the provider
func (p *EdgeCenterProvider) AdjustEndpoints(endpoints []*endpoint.Endpoint) []*endpoint.Endpoint {
	return endpoints
}

// getZoneAndRecordName returns the zone and record name for a given DNS name.
// It selects the parent zone for TXT records that manage the zone itself (e.g., a-test.example.com for zone test.example.com)
// and the most specific zone for all other records. It always returns the FQDN for the record name.
func (p *EdgeCenterProvider) getZoneAndRecordName(dnsName string, zoneNameMap map[string]string) (string, string) {
	p.logger.Info("Finding zone and record name", zap.String("dnsName", dnsName))

	originalDNSName := dnsName // Keep original for processing and logging

	// Normalize input dnsName
	dnsNameWithDot := originalDNSName
	if !strings.HasSuffix(dnsNameWithDot, ".") {
		dnsNameWithDot = dnsNameWithDot + "."
		p.logger.Info("Added trailing dot to DNS name", zap.String("correctedDNSName", dnsNameWithDot))
	}

	var bestMatch string
	var longestMatch string = ""
	longestLength := 0
	var shortestMatch string = ""
	shortestLength := -1

	// --- Determine if it's a TXT record for the zone itself ---
	isZoneTxtRecord := false
	managedZoneName := "" // The zone name potentially managed by this TXT record

	prefixes := []string{"a-", "aaaa-", "cname-", "txt-", "mx-", "ns-"} // Standard external-dns prefixes
	for _, prefix := range prefixes {
		if strings.HasPrefix(originalDNSName, prefix) {
			potentialManagedZone := strings.TrimPrefix(originalDNSName, prefix)
			potentialManagedZone = strings.TrimSuffix(potentialManagedZone, ".")
			if _, ok := zoneNameMap[potentialManagedZone]; ok {
				// It matches the pattern prefix-<zone_in_map>
				isZoneTxtRecord = true
				managedZoneName = potentialManagedZone
				p.logger.Debug("Detected TXT record potentially managing a zone",
					zap.String("dnsName", originalDNSName),
					zap.String("managedZone", managedZoneName))
				break
			}
		}
	}

	// --- Find the best matching zone based on the record type ---
	if isZoneTxtRecord {
		// Find the SHORTEST zone that is a suffix (the parent zone)
		p.logger.Debug("Applying SHORTEST zone logic for zone TXT record")
		for zoneName := range zoneNameMap {
			// Skip the zone that the TXT record is managing itself
			if zoneName == managedZoneName {
				continue
			}
			zoneWithDot := zoneName + "."
			if strings.HasSuffix(dnsNameWithDot, zoneWithDot) {
				if shortestLength == -1 || len(zoneName) < shortestLength {
					shortestMatch = zoneName
					shortestLength = len(zoneName)
					p.logger.Debug("Found potential shortest (parent) zone", zap.String("zone", zoneName), zap.String("dnsName", originalDNSName))
				}
			}
		}
		if shortestLength != -1 {
			bestMatch = shortestMatch
		} else {
			// Fallback if no shorter zone found (e.g., only the managed zone itself is in the map) - this might indicate an issue
			p.logger.Warn("Could not find a shorter parent zone for zone TXT record, falling back to longest match logic", zap.String("dnsName", originalDNSName), zap.String("managedZone", managedZoneName))
			// Proceed to longest match logic below as a fallback
		}
	}

	// If not a zone TXT record OR fallback needed, find the LONGEST zone
	if bestMatch == "" {
		if !isZoneTxtRecord {
			p.logger.Debug("Applying LONGEST zone logic for regular record")
		}
		for zoneName := range zoneNameMap {
			zoneWithDot := zoneName + "."
			if strings.HasSuffix(dnsNameWithDot, zoneWithDot) {
				if len(zoneName) > longestLength {
					longestMatch = zoneName
					longestLength = len(zoneName)
					p.logger.Debug("Found potential longest zone", zap.String("zone", zoneName), zap.String("dnsName", originalDNSName))
				}
			}
		}
		if longestLength > 0 {
			bestMatch = longestMatch
		}
	}

	// --- Handle result ---
	if bestMatch == "" {
		p.logger.Warn("No matching zone found", zap.String("dnsName", originalDNSName))
		return "", ""
	}

	p.logger.Info("Selected matching zone",
		zap.String("zone", bestMatch),
		zap.String("dnsName", originalDNSName),
		zap.Bool("isZoneTxtRecord", isZoneTxtRecord),
	)

	// Убираем точку в конце из исходного имени для возврата
	recordName := strings.TrimSuffix(originalDNSName, ".")
	// Зону тоже возвращаем без точки
	bestMatch = strings.TrimSuffix(bestMatch, ".")

	p.logger.Info("Returning result",
		zap.String("zone", bestMatch),
		zap.String("recordName", recordName))
	return bestMatch, recordName
}
