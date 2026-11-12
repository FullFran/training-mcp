package training

import (
	"context"
	"math"
	"strings"
)

// Weight units. Every weight is stored in kg, as it always was; a unit only
// says how a person types and reads it. A person has a default unit (kg until
// they choose), and any input can still be typed in the other one.
const (
	UnitKG = "kg"
	UnitLB = "lb"
)

// LbToKG is the exact international pound.
const LbToKG = 0.45359237

// snapGrids are the kg steps a value typed in lb is snapped to, coarsest first.
var snapGrids = []float64{2.5, 1.25, 0.5, 0.25, 0.05, 0.01}

// NormalizeUnit reads a unit as sent by a form or a tool: "" is kg (what every
// existing caller means), case is ignored, and anything else is "".
func NormalizeUnit(unit string) string {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "", UnitKG:
		return UnitKG
	case UnitLB:
		return UnitLB
	}
	return ""
}

// ToKG converts a weight typed in unit to the kg stored, at the store's 0.01 kg.
//
// A value typed in lb is snapped to the roundest kg value that still shows as
// the same lb number (FromKG keeps lb to 0.1). So a load logged in kg and shown
// in lb is saved back unchanged (60 kg shows as 132.3 lb, which stores 60 kg,
// not 60.01), and a value typed in lb shows again as typed.
func ToKG(value float64, unit string) float64 {
	if unit != UnitLB {
		return RoundWeight(value)
	}
	raw := value * LbToKG
	// The kg values that show as this lb number, with a hair of margin so a
	// boundary rounding the same way FromKG does is never lost to float noise.
	lo, hi := (value-0.05)*LbToKG, (value+0.05)*LbToKG
	for _, grid := range snapGrids {
		c := RoundWeight(math.Round(raw/grid) * grid)
		if c > lo && c < hi && FromKG(c, UnitLB) == math.Round(value*10)/10 {
			return c
		}
	}
	return RoundWeight(raw)
}

// FromKG is a stored kg weight in unit: kg to 0.01 as stored, lb to 0.1 lb.
func FromKG(kg float64, unit string) float64 {
	if unit != UnitLB {
		return RoundWeight(kg)
	}
	return math.Round(kg/LbToKG*10) / 10
}

// PlateStep is the smallest usual load change in a unit: 2.5 kg or 5 lb.
func PlateStep(unit string) float64 {
	if unit == UnitLB {
		return 5
	}
	return 2.5
}

// WeightUnit is the person's default unit, kg until they choose.
func (s *Service) WeightUnit(ctx context.Context) (string, error) {
	v, err := s.store.WeightUnit(ctx)
	if err != nil || v != UnitLB {
		return UnitKG, err
	}
	return UnitLB, nil
}

// SetWeightUnit stores the person's default unit: kg or lb.
func (s *Service) SetWeightUnit(ctx context.Context, unit string) error {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case UnitKG:
		return s.store.SetWeightUnit(ctx, UnitKG)
	case UnitLB:
		return s.store.SetWeightUnit(ctx, UnitLB)
	}
	return ErrValidation
}
