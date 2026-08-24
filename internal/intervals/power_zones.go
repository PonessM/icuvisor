package intervals

import "fmt"

// PowerZoneValidationCode identifies why power zones cannot be normalized.
type PowerZoneValidationCode string

const (
	// PowerZoneValid means the power zones were normalized successfully.
	PowerZoneValid PowerZoneValidationCode = ""
	// PowerZoneMissingZones means no power-zone ceilings were configured.
	PowerZoneMissingZones PowerZoneValidationCode = "missing_power_zones"
	// PowerZoneMissingFTP means the configured FTP is not positive.
	PowerZoneMissingFTP PowerZoneValidationCode = "missing_power_ftp"
	// PowerZoneInvalidCeilings means power-zone ceilings are not strictly increasing positive values.
	PowerZoneInvalidCeilings PowerZoneValidationCode = "invalid_power_zone_ceilings"
	// PowerZoneMismatchedNames means configured power-zone names do not match the ceiling count.
	PowerZoneMismatchedNames PowerZoneValidationCode = "mismatched_power_zone_names"
)

// NormalizedPowerZones contains upstream power-zone ceilings and their analyzer-ready watt boundaries.
type NormalizedPowerZones struct {
	UpperBoundsPercentOfFTP  []int
	UpperBoundsWatts         []float64
	AnalyzerLowerBoundsWatts []float64
	AnalyzerNames            []string
}

// NormalizePowerZones validates and derives upstream percentage-based power zones.
func NormalizePowerZones(ftpWatts int, upperBoundsPercentOfFTP []int, names []string) (NormalizedPowerZones, PowerZoneValidationCode) {
	result := NormalizedPowerZones{
		UpperBoundsPercentOfFTP: append([]int(nil), upperBoundsPercentOfFTP...),
	}
	if len(upperBoundsPercentOfFTP) == 0 {
		return result, PowerZoneMissingZones
	}

	for i, ceiling := range upperBoundsPercentOfFTP {
		if ceiling <= 0 || (i > 0 && ceiling <= upperBoundsPercentOfFTP[i-1]) {
			return result, PowerZoneInvalidCeilings
		}
	}
	if len(names) > 0 && len(names) != len(upperBoundsPercentOfFTP) {
		return result, PowerZoneMismatchedNames
	}
	if ftpWatts <= 0 {
		return result, PowerZoneMissingFTP
	}

	result.UpperBoundsWatts = make([]float64, len(upperBoundsPercentOfFTP))
	result.AnalyzerLowerBoundsWatts = make([]float64, 1, len(upperBoundsPercentOfFTP)+1)
	result.AnalyzerNames = make([]string, 0, len(upperBoundsPercentOfFTP)+1)
	result.AnalyzerLowerBoundsWatts[0] = 0
	for i, ceiling := range upperBoundsPercentOfFTP {
		watts := float64(ftpWatts) * float64(ceiling) / 100
		result.UpperBoundsWatts[i] = watts
		result.AnalyzerLowerBoundsWatts = append(result.AnalyzerLowerBoundsWatts, watts)
		if len(names) == 0 {
			result.AnalyzerNames = append(result.AnalyzerNames, fmt.Sprintf("Zone %d", i+1))
			continue
		}
		result.AnalyzerNames = append(result.AnalyzerNames, names[i])
	}
	result.AnalyzerNames = append(result.AnalyzerNames, "Above "+result.AnalyzerNames[len(result.AnalyzerNames)-1])

	return result, PowerZoneValid
}
