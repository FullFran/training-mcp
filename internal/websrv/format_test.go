package websrv

import "testing"

func TestFormatNumberRoundsToTwoDecimalsWithoutTrailingZeros(t *testing.T) {
	for in, want := range map[float64]string{
		65.77089365: "65.77",
		100:         "100",
		102.5:       "102.5",
		8.25:        "8.25",
		45.359237:   "45.36",
		0.004:       "0",
	} {
		if got := formatNumber(in); got != want {
			t.Fatalf("formatNumber(%v) = %q, want %q", in, got, want)
		}
	}
}
