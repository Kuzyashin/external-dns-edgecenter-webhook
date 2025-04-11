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
	zones, err := p.client.Zones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get zones: %w", err)
	}

	endpoints := []*endpoint.Endpoint{}
	zoneNameMap := make(map[string]string)

	for _, zone := range zones {
		if !p.domainFilter.Match(zone.Name) {
			continue
		}
		zoneNameMap[zone.Name] = zone.Name

		zoneDetails, err := p.client.Zone(ctx, zone.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get zone %s details: %w", zone.Name, err)
		}

		for _, record := range zoneDetails.Records {
			if record.Type == "SOA" || record.Type == "NS" {
				continue
			}

			name := record.Name
			if name == "@" {
				name = zone.Name
			} else {
				name = fmt.Sprintf("%s.%s", name, zone.Name)
			}
			if !strings.HasSuffix(name, ".") {
				name = name + "."
			}
			p.logger.Debug("Creating endpoint", zap.String("name", name))

			ep := endpoint.NewEndpoint(
				name,
				record.Type,
				record.ShortAnswers...,
			)
			ep.RecordTTL = endpoint.TTL(record.TTL)
			ep.DNSName = name
			p.logger.Debug("Created endpoint", zap.String("name", name), zap.String("dnsName", ep.DNSName))
			endpoints = append(endpoints, ep)
		}
	}

	return endpoints, nil
}

// ApplyChanges applies the given changes to the EdgeCenter DNS
func (p *EdgeCenterProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	zones, err := p.client.Zones(ctx, func(filter *dnssdk.ZonesFilter) {
		filter.Names = p.domainFilter.Filters
	})
	if err != nil {
		return fmt.Errorf("failed to list zones: %v", err)
	}

	zoneNameMap := make(map[string]string)
	for _, z := range zones {
		zoneNameMap[z.Name] = z.Name
	}

	for _, change := range changes.Create {
		zoneName, recordName := p.getZoneAndRecordName(change.DNSName, zoneNameMap)
		if zoneName == "" {
			p.logger.Warn("no matching zone found for record", zap.String("record", change.DNSName))
			continue
		}

		ttl := int(change.RecordTTL)
		if ttl == 0 {
			ttl = 3600 // Default TTL
		}

		content := dnssdk.ToRecordType(change.RecordType, change.Targets[0])
		if p.dryRun {
			p.logger.Info("would create record",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Any("content", content.ToContent()),
				zap.Int("ttl", ttl))
			continue
		}

		err := p.client.AddZoneRRSet(ctx, zoneName, recordName, change.RecordType, []dnssdk.ResourceRecord{
			{Content: content.ToContent()},
		}, ttl)
		if err != nil {
			return fmt.Errorf("failed to create record %s: %v", change.DNSName, err)
		}
	}

	for _, change := range changes.UpdateNew {
		zoneName, recordName := p.getZoneAndRecordName(change.DNSName, zoneNameMap)
		if zoneName == "" {
			p.logger.Warn("no matching zone found for record", zap.String("record", change.DNSName))
			continue
		}

		ttl := int(change.RecordTTL)
		if ttl == 0 {
			ttl = 3600 // Default TTL
		}

		if p.dryRun {
			p.logger.Info("would update record",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType),
				zap.Strings("targets", change.Targets),
				zap.Int("ttl", ttl))
			continue
		}

		// First, delete the old record
		err := p.client.DeleteRRSet(ctx, zoneName, recordName, change.RecordType)
		if err != nil {
			return fmt.Errorf("failed to delete old record %s: %v", change.DNSName, err)
		}

		// Then create the new record
		content := dnssdk.ToRecordType(change.RecordType, change.Targets[0])
		err = p.client.AddZoneRRSet(ctx, zoneName, recordName, change.RecordType, []dnssdk.ResourceRecord{
			{Content: content.ToContent()},
		}, ttl)
		if err != nil {
			return fmt.Errorf("failed to update record %s: %v", change.DNSName, err)
		}
	}

	for _, change := range changes.Delete {
		zoneName, recordName := p.getZoneAndRecordName(change.DNSName, zoneNameMap)
		if zoneName == "" {
			p.logger.Warn("no matching zone found for record", zap.String("record", change.DNSName))
			continue
		}

		if p.dryRun {
			p.logger.Info("would delete record",
				zap.String("zone", zoneName),
				zap.String("record", recordName),
				zap.String("type", change.RecordType))
			continue
		}

		err := p.client.DeleteRRSet(ctx, zoneName, recordName, change.RecordType)
		if err != nil {
			return fmt.Errorf("failed to delete record %s: %v", change.DNSName, err)
		}
	}

	return nil
}

// AdjustEndpoints adjusts the endpoints for the provider
func (p *EdgeCenterProvider) AdjustEndpoints(endpoints []*endpoint.Endpoint) []*endpoint.Endpoint {
	return endpoints
}

// getZoneAndRecordName returns the zone and record name for a given DNS name
func (p *EdgeCenterProvider) getZoneAndRecordName(dnsName string, zoneNameMap map[string]string) (string, string) {
	if !strings.HasSuffix(dnsName, ".") {
		dnsName = dnsName + "."
	}

	var longestMatch string
	longestLength := 0

	for zoneName := range zoneNameMap {
		if strings.HasSuffix(dnsName, "."+zoneName+".") {
			if len(zoneName) > longestLength {
				longestMatch = zoneName
				longestLength = len(zoneName)
			}
		}
	}

	if longestLength == 0 {
		return "", ""
	}

	recordName := strings.TrimSuffix(dnsName, "."+longestMatch+".")
	return longestMatch, recordName
}
