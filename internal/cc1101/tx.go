package cc1101

import (
	"context"
	"errors"
)

// TransmitPulses tunes CC1101 to freqHz with preset, enters async serial TX,
// keys GDO0 according to signed microsecond pulses (+high / -low), and
// unconditionally returns GDO0 to high-Z and CC1101 to IDLE on exit or cancel.
func (r *Radio) TransmitPulses(ctx context.Context, freqHz uint32, preset Preset, pulses []int32) error {
	if len(pulses) == 0 {
		return errors.New("cc1101: empty pulse train")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.preset != preset {
		if err := r.ApplyPreset(preset); err != nil {
			return err
		}
	}
	if err := r.SetDataOutput(true); err != nil {
		return err
	}
	defer func() {
		_ = r.SetDataOutput(false)
		_ = r.Idle()
	}()

	if err := r.StartTransmit(freqHz); err != nil {
		return err
	}
	return r.tr.TransmitPulses(ctx, pulses)
}
