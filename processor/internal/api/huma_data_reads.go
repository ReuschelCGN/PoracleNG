package api

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/pokemon/poracleng/processor/internal/geocoding"
)

// anyBodyOutput is a huma output whose body re-marshals an arbitrary value to
// the same JSON the legacy gin handlers produced via c.JSON.
type anyBodyOutput struct {
	Body any
}

// weatherCellInput carries the required cell query param for the weather read.
type weatherCellInput struct {
	Cell string `query:"cell" required:"true"`
}

// RegisterWeather registers GET /api/weather, serving the per-cell weather map.
// Replaces the legacy gin HandleWeather. A missing cell param now yields a
// problem+json 422 (huma's required-validation) instead of the legacy 400.
func RegisterWeather(api huma.API, weather WeatherExporter) {
	huma.Register(api, huma.Operation{
		OperationID: "get-weather", Method: "GET", Path: "/weather",
		Summary: "Get weather data for an S2 cell", Tags: []string{"weather"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *weatherCellInput) (*anyBodyOutput, error) {
		return &anyBodyOutput{Body: weather.ExportCellWeather(in.Cell)}, nil
	})
}

// RegisterStats registers a no-input stats read op at the given path that
// JSON-encodes the result of export(). Replaces the legacy gin HandleStats.
func RegisterStats(api huma.API, opID, path string, export func() any) {
	huma.Register(api, huma.Operation{
		OperationID: opID, Method: "GET", Path: path,
		Summary: "Get statistics", Tags: []string{"stats"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, _ *struct{}) (*anyBodyOutput, error) {
		return &anyBodyOutput{Body: export()}, nil
	})
}

// ForwardGeocoder performs a forward geocode lookup. *geocoding.Geocoder
// satisfies this; a minimal interface keeps the Register signature testable.
type ForwardGeocoder interface {
	Forward(query string) ([]geocoding.ForwardResult, error)
}

// geocodeQueryInput carries the required q query param for the forward geocode.
type geocodeQueryInput struct {
	Q string `query:"q" required:"true"`
}

// RegisterGeocode registers GET /api/geocode/forward, performing a forward
// geocode lookup. Replaces the legacy gin HandleGeocode. A missing/empty q now
// yields a problem+json 422 (huma's required-validation) instead of the legacy
// 400; a nil geocoder yields 503; lookup errors yield 500.
func RegisterGeocode(api huma.API, geocoder ForwardGeocoder) {
	huma.Register(api, huma.Operation{
		OperationID: "get-geocode-forward", Method: "GET", Path: "/geocode/forward",
		Summary: "Forward geocode lookup", Tags: []string{"geocode"},
		Security: []map[string][]string{{"poracleSecret": {}}},
	}, func(_ context.Context, in *geocodeQueryInput) (*anyBodyOutput, error) {
		// Guard against both an interface-nil and a typed-nil
		// *geocoding.Geocoder (proc.enricher.Geocoder is nil when geocoding
		// is disabled), matching the legacy concrete nil check → 503.
		if geocoder == nil {
			return nil, huma.Error503ServiceUnavailable("geocoder not configured")
		}
		if g, ok := geocoder.(*geocoding.Geocoder); ok && g == nil {
			return nil, huma.Error503ServiceUnavailable("geocoder not configured")
		}
		results, err := geocoder.Forward(in.Q)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &anyBodyOutput{Body: results}, nil
	})
}
