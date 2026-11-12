package training

import (
	"math"
	"testing"
)

// A weight typed in lb is stored in kg and shows again as the same lb number,
// and saving that number again stores the same kg: no drift either way.
func TestWeightUnitsRoundTripWithoutDrift(t *testing.T) {
	for tenths := 1; tenths <= 15000; tenths++ { // 0.1 to 1500 lb
		lb := float64(tenths) / 10
		kg := ToKG(lb, UnitLB)
		if shown := FromKG(kg, UnitLB); shown != lb {
			t.Fatalf("%v lb stored as %v kg shows %v lb", lb, kg, shown)
		}
		if again := ToKG(FromKG(kg, UnitLB), UnitLB); again != kg {
			t.Fatalf("%v lb: saving the shown value again moved %v kg to %v kg", lb, kg, again)
		}
	}
	// A load logged in kg and shown in lb is saved back unchanged: 60 kg shows
	// as 132.3 lb and saving 132.3 lb stores 60 kg, not 60.01.
	for quarters := 1; quarters <= 2000; quarters++ { // 0.25 to 500 kg
		kg := float64(quarters) / 4
		if back := ToKG(FromKG(kg, UnitLB), UnitLB); back != kg {
			t.Fatalf("%v kg shown as %v lb is saved as %v kg", kg, FromKG(kg, UnitLB), back)
		}
	}
	if got := FromKG(60, UnitLB); got != 132.3 {
		t.Fatalf("60 kg = %v lb", got)
	}
	if got := ToKG(135, UnitLB); math.Abs(got-61.23) > 0.03 {
		t.Fatalf("135 lb = %v kg", got)
	}
	// kg is as stored, to the 0.01 kg the store keeps.
	if got := ToKG(62.555, UnitKG); got != 62.56 {
		t.Fatalf("62.555 kg = %v", got)
	}
	if got := FromKG(62.5, UnitKG); got != 62.5 {
		t.Fatalf("62.5 kg shows %v", got)
	}
	// Unknown units read as kg, the unit every existing caller uses.
	if NormalizeUnit("") != UnitKG || NormalizeUnit("LB") != UnitLB || NormalizeUnit("stone") != "" {
		t.Fatal("NormalizeUnit")
	}
}

// Load steps are plates: 2.5 kg, or 5 lb.
func TestPlateStep(t *testing.T) {
	if PlateStep(UnitKG) != 2.5 || PlateStep(UnitLB) != 5 {
		t.Fatal("PlateStep")
	}
}
