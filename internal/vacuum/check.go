package vacuum

import "fmt"

// StableCeiling is the pressure ceiling required for a stable vacuum permit.
const StableCeiling = 0.001

type VacuumFault struct {
	Pressure float64
	Reason   string
}

func (f *VacuumFault) Error() string {
	if f == nil {
		return "<nil>"
	}
	return fmt.Sprintf("vacuum pressure %.6g: %s", f.Pressure, f.Reason)
}

func Check(c *Controller) error {
	if c.Stable() && c.Pressure() < StableCeiling {
		return nil
	}
	return &VacuumFault{Pressure: c.Pressure(), Reason: "pressure not stable"}
}
