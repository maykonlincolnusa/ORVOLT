package contract

import (
	"math"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	sitev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/energy/site/v1"
)

var energyProviders = map[sitev1.EnergyProvider]string{
	sitev1.EnergyProvider_ENERGY_PROVIDER_SMA:          "SMA",
	sitev1.EnergyProvider_ENERGY_PROVIDER_ENPHASE:      "ENPHASE",
	sitev1.EnergyProvider_ENERGY_PROVIDER_SOLAREDGE:    "SOLAREDGE",
	sitev1.EnergyProvider_ENERGY_PROVIDER_TESLA_ENERGY: "TESLA_ENERGY",
	sitev1.EnergyProvider_ENERGY_PROVIDER_UTILITY:      "UTILITY",
	sitev1.EnergyProvider_ENERGY_PROVIDER_OTHER:        "OTHER",
}

var dataQualities = map[sitev1.DataQuality]string{
	sitev1.DataQuality_DATA_QUALITY_MEASURED:    "MEASURED",
	sitev1.DataQuality_DATA_QUALITY_ESTIMATED:   "ESTIMATED",
	sitev1.DataQuality_DATA_QUALITY_FORECAST:    "FORECAST",
	sitev1.DataQuality_DATA_QUALITY_UNAVAILABLE: "UNAVAILABLE",
}

func DecodeEnergySiteObservation(payload []byte, ingestedAt time.Time) (domain.EnergyObservation, error) {
	var message sitev1.EnergySiteObservation
	if err := proto.Unmarshal(payload, &message); err != nil {
		return domain.EnergyObservation{}, permanent("decode energy site observation: %v", err)
	}
	if message.GetSiteId() == "" || message.GetObservedAtMs() <= 0 || message.GetRetrievedAtMs() <= 0 {
		return domain.EnergyObservation{}, permanent("energy observation is missing required timestamps or site identity")
	}
	source := message.GetSource()
	if source == nil || source.GetProviderSiteId() == "" || source.GetConsentScope() == "" {
		return domain.EnergyObservation{}, permanent("energy observation is missing required provider provenance")
	}
	provider, known := energyProviders[source.GetProvider()]
	if !known {
		return domain.EnergyObservation{}, permanent("energy observation provider is unspecified or unknown")
	}
	quality, known := dataQualities[message.GetDataQuality()]
	if !known {
		return domain.EnergyObservation{}, permanent("energy observation data quality is unspecified or unknown")
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
		ProviderSiteID:     source.GetProviderSiteId(),
		ProviderAssetID:    source.GetProviderAssetId(),
		ConsentScope:       source.GetConsentScope(),
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
		IngestedAt:         ingestedAt,
	}, nil
}

func validateNonNegative(value *float64) error {
	if value != nil && (!isFinite(*value) || *value < 0) {
		return permanent("energy observation power or tariff value must be finite and non-negative")
	}
	return nil
}

func validateRange(value *float64, minimum, maximum float64) error {
	if value != nil && (!isFinite(*value) || *value < minimum || *value > maximum) {
		return permanent("energy observation value must be finite and between %v and %v", minimum, maximum)
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
