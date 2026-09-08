package cli

import (
	"fmt"

	"github.com/go-rod/rod/lib/proto"
	"github.com/spf13/cobra"
)

var (
	flagGeoLat      float64
	flagGeoLng      float64
	flagGeoAccuracy float64
)

var geolocationCmd = &cobra.Command{
	Use:   "geolocation",
	Short: "Override or clear the browser's geolocation",
	Long: `Override navigator.geolocation for the current session. Like other
emulation overrides, this is session-scoped — use inside a batch flow with
"emulate" to cover multi-step agent runs.

Examples:
  ghostchrome geolocation set --lat 48.8566 --lng 2.3522
  ghostchrome geolocation set --lat 35.6762 --lng 139.6503 --accuracy 10
  ghostchrome geolocation clear`,
}

var geolocationSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set an override lat/lng (accuracy optional)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if !cmd.Flag("lat").Changed || !cmd.Flag("lng").Changed {
			exitErr("geolocation set", fmt.Errorf("--lat and --lng are required"))
		}

		b, page := openPage()
		defer b.Close()

		req := proto.EmulationSetGeolocationOverride{
			Latitude:  &flagGeoLat,
			Longitude: &flagGeoLng,
			Accuracy:  &flagGeoAccuracy,
		}
		if err := req.Call(page); err != nil {
			exitErr("geolocation set", err)
		}

		type setResult struct {
			Action    string  `json:"action"`
			Latitude  float64 `json:"lat"`
			Longitude float64 `json:"lng"`
			Accuracy  float64 `json:"accuracy"`
		}
		text := fmt.Sprintf("Geolocation overridden: (%.4f, %.4f) ±%.0fm", flagGeoLat, flagGeoLng, flagGeoAccuracy)
		output(&setResult{Action: "set", Latitude: flagGeoLat, Longitude: flagGeoLng, Accuracy: flagGeoAccuracy}, text)
	},
}

var geolocationClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear any geolocation override",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		if err := (proto.EmulationClearGeolocationOverride{}).Call(page); err != nil {
			exitErr("geolocation clear", err)
		}

		type clearResult struct {
			Action string `json:"action"`
		}
		output(&clearResult{Action: "clear"}, "Geolocation override cleared")
	},
}

func init() {
	geolocationSetCmd.Flags().Float64Var(&flagGeoLat, "lat", 0, "Latitude in decimal degrees")
	geolocationSetCmd.Flags().Float64Var(&flagGeoLng, "lng", 0, "Longitude in decimal degrees")
	geolocationSetCmd.Flags().Float64Var(&flagGeoAccuracy, "accuracy", 100, "Accuracy in meters (default 100)")

	geolocationCmd.AddCommand(geolocationSetCmd, geolocationClearCmd)
	rootCmd.AddCommand(geolocationCmd)
}
