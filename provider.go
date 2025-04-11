package main

import (
	"context"
	"fmt"
	"strings"

	dnssdk "github.com/Edge-Center/edgecenter-dns-sdk-go"
	"go.uber.org/zap"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
)

// DNSClient определяет интерфейс для клиента DNS
type DNSClient interface {
	Zones(ctx context.Context, filters ...func(*dnssdk.ZonesFilter)) ([]dnssdk.Zone, error)
	Zone(ctx context.Context, name string) (dnssdk.Zone, error)
	AddZoneRRSet(ctx context.Context, zoneName, recordName, recordType string, records []dnssdk.ResourceRecord, ttl int, opts ...dnssdk.AddZoneOpt) error
	DeleteRRSet(ctx context.Context, zoneName, recordName, recordType string) error
}

// Provider определяет интерфейс для DNS провайдера
type Provider interface {
	Records(ctx context.Context) ([]*endpoint.Endpoint, error)
	ApplyChanges(ctx context.Context, changes *plan.Changes) error
	AdjustEndpoints(endpoints []*endpoint.Endpoint) []*endpoint.Endpoint
	GetDomainFilter() []string
}

// EdgeCenterProvider implements the ExternalDNS provider interface for EdgeCenter DNS
type EdgeCenterProvider struct {
	client       DNSClient
	domainFilter endpoint.DomainFilter
	logger       *zap.Logger
	dryRun       bool
}

// NewEdgeCenterProvider creates a new EdgeCenter DNS provider
func NewEdgeCenterProvider(client DNSClient, domainFilter endpoint.DomainFilter, logger *zap.Logger, dryRun bool) (Provider, error) {
	return &EdgeCenterProvider{
		client:       client,
		domainFilter: domainFilter,
		logger:       logger,
		dryRun:       dryRun,
	}, nil
}

// GetDomainFilter returns the list of domain filters
func (p *EdgeCenterProvider) GetDomainFilter() []string {
	return p.domainFilter.Filters
}

// Records returns the list of records in all relevant zones
func (p *EdgeCenterProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	p.logger.Info("Fetching DNS records from EdgeCenter")
	zones, err := p.client.Zones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get zones: %w", err)
	}
	p.logger.Info("Retrieved zones from EdgeCenter", zap.Int("count", len(zones)))

	endpoints := []*endpoint.Endpoint{}
	zoneNameMap := make(map[string]string)

	for _, zone := range zones {
		if !p.domainFilter.Match(zone.Name) {
			p.logger.Info("Skipping zone - not in domain filter", zap.String("zone", zone.Name))
			continue
		}
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
			ttl = 600 // Default TTL
			p.logger.Info("Using default TTL", zap.Int("ttl", ttl))
		}

		content := dnssdk.ToRecordType(change.RecordType, change.Targets[0])
		if p.dryRun {
			p.logger.Info("Would create record (dry-run)",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Any("content", content.ToContent()),
				zap.Int("ttl", ttl))
			continue
		}

		p.logger.Info("Creating DNS record",
			zap.String("original_name", change.DNSName),
			zap.String("zone", zoneName),
			zap.String("record", recordName),
			zap.String("type", change.RecordType),
			zap.Any("content", content.ToContent()),
			zap.Int("ttl", ttl))

		err := p.client.AddZoneRRSet(ctx, zoneName, recordName, change.RecordType, []dnssdk.ResourceRecord{
			{Content: content.ToContent(), Enabled: true},
		}, ttl)
		if err != nil {
			return fmt.Errorf("failed to create record %s: %v", change.DNSName, err)
		}
		p.logger.Info("Successfully created DNS record",
			zap.String("zone", zoneName),
			zap.String("record", recordName))
	}

	// Обработка обновления записей
	for _, change := range changes.UpdateNew {
		p.logger.Info("Processing update request",
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
			ttl = 600 // Default TTL
			p.logger.Info("Using default TTL for update", zap.Int("ttl", ttl))
		}

		if p.dryRun {
			p.logger.Info("Would update record (dry-run)",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Strings("targets", change.Targets),
				zap.Int("ttl", ttl))
			continue
		}

		p.logger.Info("Deleting old record for update",
			zap.String("zone", zoneName),
			zap.String("record", recordName),
			zap.String("type", change.RecordType))

		err := p.client.DeleteRRSet(ctx, zoneName, recordName, change.RecordType)
		if err != nil {
			return fmt.Errorf("failed to delete old record %s: %v", change.DNSName, err)
		}

		content := dnssdk.ToRecordType(change.RecordType, change.Targets[0])
		p.logger.Info("Creating new record for update",
			zap.String("zone", zoneName),
			zap.String("record", recordName),
			zap.String("type", change.RecordType),
			zap.Any("content", content.ToContent()))

		err = p.client.AddZoneRRSet(ctx, zoneName, recordName, change.RecordType, []dnssdk.ResourceRecord{
			{Content: content.ToContent(), Enabled: true},
		}, ttl)
		if err != nil {
			return fmt.Errorf("failed to update record %s: %v", change.DNSName, err)
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

// getZoneAndRecordName returns the zone and record name for a given DNS name
func (p *EdgeCenterProvider) getZoneAndRecordName(dnsName string, zoneNameMap map[string]string) (string, string) {
	p.logger.Info("Finding zone and record name", zap.String("dnsName", dnsName))

	if !strings.HasSuffix(dnsName, ".") {
		dnsName = dnsName + "."
		p.logger.Info("Added trailing dot to DNS name", zap.String("dnsName", dnsName))
	}

	var longestMatch string
	longestLength := 0

	for zoneName := range zoneNameMap {
		zoneWithDot := zoneName + "."
		if strings.HasSuffix(dnsName, zoneWithDot) {
			if len(zoneName) > longestLength {
				longestMatch = zoneName
				longestLength = len(zoneName)
				p.logger.Info("Found matching zone",
					zap.String("zone", zoneName),
					zap.String("dnsName", dnsName))
			}
		}
	}

	if longestLength == 0 {
		p.logger.Info("No matching zone found", zap.String("dnsName", dnsName))
		return "", ""
	}

	recordName := strings.TrimSuffix(dnsName, ".")
	p.logger.Info("Found record name",
		zap.String("zone", longestMatch),
		zap.String("recordName", recordName))
	return longestMatch, recordName
}
