package config

import (
	"fmt"
	"strings"
)

// FrameSettings contains the editable media parameters and immutable task snapshot.
type FrameSettings struct {
	Sampling SamplingConfig `json:"sampling"`
	Images   ImagesConfig   `json:"images"`
}

func DefaultFrameSettings() FrameSettings {
	c := defaults()
	return FrameSettings{Sampling: c.Sampling, Images: c.Images}
}

func (s *FrameSettings) Validate() error {
	c := Config{Sampling: s.Sampling, Images: s.Images}
	if errs := c.validateSampling(); len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "；"))
	}
	s.Sampling = c.Sampling
	return nil
}
