package contract

import (
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	sitev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/energy/site/v1"
)

func DecodeEnergySiteObservation(payload []byte) (domain.EnergyObservation, error) {
	var message sitev1.EnergySiteObservation
	if err := proto.Unmarshal(payload, &message); err != nil {
		return domain.EnergyObservation{}, fmt.Errorf("decode energy site observation: %w", err)
	}
	if message.GetSiteId() == "" || message.GetObservedAtMs() <= 0 || message.GetRetrievedAtMs() <= 0 {
		return domain.EnergyObservation{}, fmt.Errorf("energy observation is missing required timestamps or site identity")
	}
	if message.GetSource() == nil || message.GetSource().GetProviderSiteId() == "" || message.GetSource().GetConsentScope() == "" {
		return domain.EnergyObservation{}, fmt.Errorf("energy observation is missing required provider provenance")
	}
	provider := message.GetSource().GetProvider().String()
	if provider == "ENERGY_PROVIDER_UNSPECIFIED" {
		return domain.EnergyObservation{}, fmt.Errorf("energy observation provider is unspecified")
	}
	quality := message.GetDataQuality().String()
	if quality == "DATA_QUALITY_UNSPECIFIED" {
		return domain.EnergyObservation{}, fmt.Errorf("energy observation data quality is unspecified")
	}
	for _, value := range []*float64{
		message.SolarGenerationKw,
		message.SiteLoadKw,
		message.GridImportKw,
		message.GridExportKw,
		message.BatteryChargeKw,
		message.BatteryDischargeKw,
		message.TariffImportPerKwh,
	} {
		if err := validateNonNegative(value); err != nil {
			return domain.EnergyObservation{}, err
		}
	}
	if err := validateRange(message.BatterySoc, 0, 100); err != nil {
		return domain.EnergyObservation{}, err
	}

	return domain.EnergyObservation{
		SiteID:             message.GetSiteId(),
		Provider:           provider,
		ProviderSiteID:     message.GetSource().GetProviderSiteId(),
		ProviderAssetID:    message.GetSource().GetProviderAssetId(),
		ConsentScope:       message.GetSource().GetConsentScope(),
		ObservedAt:         time.UnixMilli(message.GetObservedAtMs()).UTC(),
		RetrievedAt:        time.UnixMilli(message.GetRetrievedAtMs()).UTC(),
		SolarGenerationKW:  message.SolarGenerationKw,
		SiteLoadKW:         message.SiteLoadKw,
		GridImportKW:       message.GridImportKw,
		GridExportKW:       message.GridExportKw,
		BatteryChargeKW:    message.BatteryChargeKw,
		BatteryDischargeKW: message.BatteryDischargeKw,
		BatterySOC:         message.BatterySoc,
		TariffImportPerKWh: message.TariffImportPerKwh,
		DataQuality:        quality,
	}, nil
}

func validateNonNegative(value *float64) error {
	if value != nil && (!isFinite(*value) || *value < 0) {
		return fmt.Errorf("energy observation power or tariff value must be finite and non-negative")
	}
	return nil
}

func validateRange(value *float64, minimum, maximum float64) error {
	if value != nil && (!isFinite(*value) || *value < minimum || *value > maximum) {
		return fmt.Errorf("energy observation value must be finite and between %v and %v", minimum, maximum)
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
