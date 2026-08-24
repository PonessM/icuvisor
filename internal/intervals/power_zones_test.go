package intervals

import (
	"reflect"
	"testing"
)

func TestNormalizePowerZones(t *testing.T) {
	tests := []struct {
		name     string
		ftpWatts int
		ceilings []int
		names    []string
		want     NormalizedPowerZones
		wantCode PowerZoneValidationCode
	}{
		{
			name:     "derives live power zone vector",
			ftpWatts: 228,
			ceilings: []int{55, 75, 90, 105, 120, 150, 999},
			names: []string{
				"Active Recovery", "Endurance", "Tempo", "Threshold", "VO2 Max", "Anaerobic", "Neuromuscular",
			},
			want: NormalizedPowerZones{
				UpperBoundsPercentOfFTP:  []int{55, 75, 90, 105, 120, 150, 999},
				UpperBoundsWatts:         []float64{125.4, 171, 205.2, 239.4, 273.6, 342, 2277.72},
				AnalyzerLowerBoundsWatts: []float64{0, 125.4, 171, 205.2, 239.4, 273.6, 342, 2277.72},
				AnalyzerNames:            []string{"Active Recovery", "Endurance", "Tempo", "Threshold", "VO2 Max", "Anaerobic", "Neuromuscular", "Above Neuromuscular"},
			},
			wantCode: PowerZoneValid,
		},
		{
			name:     "generates generic names when names are absent",
			ftpWatts: 200,
			ceilings: []int{50, 100},
			want: NormalizedPowerZones{
				UpperBoundsPercentOfFTP:  []int{50, 100},
				UpperBoundsWatts:         []float64{100, 200},
				AnalyzerLowerBoundsWatts: []float64{0, 100, 200},
				AnalyzerNames:            []string{"Zone 1", "Zone 2", "Above Zone 2"},
			},
			wantCode: PowerZoneValid,
		},
		{
			name:     "rejects empty ceilings",
			ftpWatts: 200,
			want:     NormalizedPowerZones{},
			wantCode: PowerZoneMissingZones,
		},
		{
			name:     "rejects zero ceiling",
			ftpWatts: 200,
			ceilings: []int{50, 0},
			want:     NormalizedPowerZones{UpperBoundsPercentOfFTP: []int{50, 0}},
			wantCode: PowerZoneInvalidCeilings,
		},
		{
			name:     "rejects negative ceiling",
			ftpWatts: 200,
			ceilings: []int{50, -1},
			want:     NormalizedPowerZones{UpperBoundsPercentOfFTP: []int{50, -1}},
			wantCode: PowerZoneInvalidCeilings,
		},
		{
			name:     "rejects duplicate ceilings",
			ftpWatts: 200,
			ceilings: []int{50, 50},
			want:     NormalizedPowerZones{UpperBoundsPercentOfFTP: []int{50, 50}},
			wantCode: PowerZoneInvalidCeilings,
		},
		{
			name:     "rejects descending ceilings",
			ftpWatts: 200,
			ceilings: []int{75, 50},
			want:     NormalizedPowerZones{UpperBoundsPercentOfFTP: []int{75, 50}},
			wantCode: PowerZoneInvalidCeilings,
		},
		{
			name:     "rejects mismatched nonempty names",
			ftpWatts: 200,
			ceilings: []int{50, 100},
			names:    []string{"Easy"},
			want:     NormalizedPowerZones{UpperBoundsPercentOfFTP: []int{50, 100}},
			wantCode: PowerZoneMismatchedNames,
		},
		{
			name:     "rejects missing FTP",
			ftpWatts: 0,
			ceilings: []int{50, 100},
			want:     NormalizedPowerZones{UpperBoundsPercentOfFTP: []int{50, 100}},
			wantCode: PowerZoneMissingFTP,
		},
		{
			name:     "prefers missing zones over all later validation failures",
			ftpWatts: 0,
			names:    []string{"unused"},
			want:     NormalizedPowerZones{},
			wantCode: PowerZoneMissingZones,
		},
		{
			name:     "prefers malformed ceilings over names and FTP",
			ftpWatts: 0,
			ceilings: []int{50, 50},
			names:    []string{"one"},
			want:     NormalizedPowerZones{UpperBoundsPercentOfFTP: []int{50, 50}},
			wantCode: PowerZoneInvalidCeilings,
		},
		{
			name:     "prefers mismatched names over missing FTP",
			ftpWatts: 0,
			ceilings: []int{50, 100},
			names:    []string{"one"},
			want:     NormalizedPowerZones{UpperBoundsPercentOfFTP: []int{50, 100}},
			wantCode: PowerZoneMismatchedNames,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, code := NormalizePowerZones(tc.ftpWatts, tc.ceilings, tc.names)
			if code != tc.wantCode {
				t.Fatalf("validation code = %q, want %q", code, tc.wantCode)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("normalized zones = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestNormalizePowerZonesDoesNotAliasInputs(t *testing.T) {
	ceilings := []int{50, 100}
	names := []string{"Easy", "Hard"}

	got, code := NormalizePowerZones(200, ceilings, names)
	if code != PowerZoneValid {
		t.Fatalf("validation code = %q, want valid", code)
	}

	ceilings[0] = 99
	names[0] = "Changed"
	if got.UpperBoundsPercentOfFTP[0] != 50 {
		t.Fatalf("result ceilings aliased input = %#v", got.UpperBoundsPercentOfFTP)
	}
	if got.AnalyzerNames[0] != "Easy" {
		t.Fatalf("result names aliased input = %#v", got.AnalyzerNames)
	}

	got.UpperBoundsPercentOfFTP[1] = 88
	got.AnalyzerNames[1] = "Changed result"
	if ceilings[1] != 100 {
		t.Fatalf("input ceilings aliased result = %#v", ceilings)
	}
	if names[1] != "Hard" {
		t.Fatalf("input names aliased result = %#v", names)
	}
}
