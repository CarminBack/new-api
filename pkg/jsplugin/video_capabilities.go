package jsplugin

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// VideoCapabilities describes the adapter's supported unified request surface.
// Omitted limits are unknown, not zero or a promise of unlimited provider support.
type VideoCapabilities struct {
	MaxPromptChars            *int     `json:"maxPromptChars,omitempty"`
	RequestFormat             string   `json:"requestFormat"`
	Resolutions               []string `json:"resolutions,omitempty"`
	Ratios                    []string `json:"ratios,omitempty"`
	MinSeconds                *int     `json:"minSeconds,omitempty"`
	MaxSeconds                *int     `json:"maxSeconds,omitempty"`
	SecondsSteps              []int    `json:"secondsSteps,omitempty"`
	Frames                    *bool    `json:"frames,omitempty"`
	MaxImages                 *int     `json:"maxImages,omitempty"`
	MaxVideos                 *int     `json:"maxVideos,omitempty"`
	MaxAudios                 *int     `json:"maxAudios,omitempty"`
	RequiredImages            *int     `json:"requiredImages,omitempty"`
	ReferenceDurationRequired *bool    `json:"referenceDurationRequired,omitempty"`
}

func copyVideoPointer[T int | bool](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (v VideoCapabilities) Clone() VideoCapabilities {
	v.Resolutions = slices.Clone(v.Resolutions)
	v.Ratios = slices.Clone(v.Ratios)
	v.SecondsSteps = slices.Clone(v.SecondsSteps)
	v.MinSeconds = copyVideoPointer(v.MinSeconds)
	v.MaxSeconds = copyVideoPointer(v.MaxSeconds)
	v.Frames = copyVideoPointer(v.Frames)
	v.MaxImages = copyVideoPointer(v.MaxImages)
	v.MaxVideos = copyVideoPointer(v.MaxVideos)
	v.MaxAudios = copyVideoPointer(v.MaxAudios)
	v.RequiredImages = copyVideoPointer(v.RequiredImages)
	v.MaxPromptChars = copyVideoPointer(v.MaxPromptChars)
	v.ReferenceDurationRequired = copyVideoPointer(v.ReferenceDurationRequired)
	return v
}

func decodeVideoCapabilities(value any, meta Meta) (map[string]VideoCapabilities, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("videoCapabilities must be an object")
	}
	result := make(map[string]VideoCapabilities, len(object))
	for name, raw := range object {
		if !slices.Contains(meta.Models, name) {
			return nil, fmt.Errorf("videoCapabilities model %q is not declared", name)
		}
		claimed := slices.ContainsFunc(meta.Protocols, func(claim ProtocolClaim) bool {
			return claim.Name == "openai_video" && (len(claim.Models) == 0 || slices.Contains(claim.Models, name))
		})
		if !claimed {
			return nil, fmt.Errorf("videoCapabilities model %q must claim openai_video", name)
		}
		fields, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("videoCapabilities %q must be an object", name)
		}
		for key, field := range fields {
			if field == nil {
				return nil, fmt.Errorf("videoCapabilities %q field %q must not be null", name, key)
			}
			switch key {
			case "maxPromptChars", "requestFormat", "resolutions", "ratios", "minSeconds", "maxSeconds", "secondsSteps", "frames", "maxImages", "maxVideos", "maxAudios", "requiredImages", "referenceDurationRequired":
			default:
				return nil, fmt.Errorf("videoCapabilities %q has unknown field %q", name, key)
			}
		}
		encoded, err := common.Marshal(fields)
		if err != nil {
			return nil, err
		}
		var caps VideoCapabilities
		if err := common.Unmarshal(encoded, &caps); err != nil {
			return nil, fmt.Errorf("videoCapabilities %q: %w", name, err)
		}
		if caps.RequestFormat != "unified_video_v1" {
			return nil, fmt.Errorf("videoCapabilities %q requestFormat must be unified_video_v1", name)
		}
		for key, limit := range map[string]*int{"minSeconds": caps.MinSeconds, "maxSeconds": caps.MaxSeconds, "maxImages": caps.MaxImages, "maxVideos": caps.MaxVideos, "maxAudios": caps.MaxAudios, "requiredImages": caps.RequiredImages} {
			if limit != nil && (*limit < 0 || ((key == "minSeconds" || key == "maxSeconds") && (*limit == 0 || *limit > constant.MaxTaskDurationSeconds))) {
				return nil, fmt.Errorf("videoCapabilities %q invalid %s", name, key)
			}
		}
		if caps.MaxPromptChars != nil && *caps.MaxPromptChars <= 0 {
			return nil, fmt.Errorf("videoCapabilities %q invalid maxPromptChars", name)
		}
		if caps.MinSeconds != nil && caps.MaxSeconds != nil && *caps.MinSeconds > *caps.MaxSeconds {
			return nil, fmt.Errorf("videoCapabilities %q has an empty duration range", name)
		}
		if caps.RequiredImages != nil && caps.MaxImages != nil && *caps.RequiredImages > *caps.MaxImages {
			return nil, fmt.Errorf("videoCapabilities %q requiredImages exceeds maxImages", name)
		}
		if caps.Frames != nil && *caps.Frames && caps.MaxImages != nil && *caps.MaxImages < 2 {
			return nil, fmt.Errorf("videoCapabilities %q frames require two images", name)
		}
		for key, pattern := range map[string]string{"resolutions": `^(?:[1-9][0-9]*p|4K)$`, "ratios": `^[1-9][0-9]{0,3}:[1-9][0-9]{0,3}$`} {
			if _, present := fields[key]; !present {
				continue
			}
			items, err := strictStringSlice(fields, key)
			if err != nil || len(items) == 0 {
				return nil, fmt.Errorf("videoCapabilities %q %s must be a non-empty string array", name, key)
			}
			seen := make(map[string]bool)
			for _, item := range items {
				if !regexp.MustCompile(pattern).MatchString(item) || seen[item] {
					return nil, fmt.Errorf("videoCapabilities %q invalid or duplicate %s value", name, key)
				}
				seen[item] = true
			}
		}
		if _, present := fields["secondsSteps"]; present {
			if len(caps.SecondsSteps) == 0 {
				return nil, fmt.Errorf("videoCapabilities %q secondsSteps must not be empty", name)
			}
			for i, seconds := range caps.SecondsSteps {
				if seconds <= 0 || seconds > constant.MaxTaskDurationSeconds || (i > 0 && seconds <= caps.SecondsSteps[i-1]) || (caps.MinSeconds != nil && seconds < *caps.MinSeconds) || (caps.MaxSeconds != nil && seconds > *caps.MaxSeconds) {
					return nil, fmt.Errorf("videoCapabilities %q secondsSteps must be ordered, unique and within the duration range", name)
				}
			}
		}
		result[name] = caps
	}
	return result, nil
}

// VideoCapabilitiesForModel exposes only a common contract for every active
// provider claiming this video model. An undeclared provider or empty common
// capability set suppresses the declaration rather than choosing one at random.
func (g *RoutingGeneration) VideoCapabilitiesForModel(model string) *VideoCapabilities {
	canonical, ok := g.CanonicalModel(model)
	if !ok {
		return nil
	}
	candidates := g.LookupEndpointCandidates("POST", "/v1/videos", canonical)
	var result *VideoCapabilities
	for _, candidate := range candidates {
		caps, exists := candidate.Plugin.Meta.VideoCapabilities[canonical]
		if !exists {
			return nil
		}
		if result == nil {
			cloned := caps.Clone()
			result = &cloned
			continue
		}
		for _, pair := range [][2]*[]string{{&result.Resolutions, &caps.Resolutions}, {&result.Ratios, &caps.Ratios}} {
			if *pair[0] == nil {
				*pair[0] = slices.Clone(*pair[1])
				continue
			}
			if *pair[1] == nil {
				continue
			}
			*pair[0] = slices.DeleteFunc(*pair[0], func(value string) bool { return !slices.Contains(*pair[1], value) })
			if len(*pair[0]) == 0 {
				return nil
			}
		}
		if caps.MinSeconds != nil && (result.MinSeconds == nil || *caps.MinSeconds > *result.MinSeconds) {
			result.MinSeconds = copyVideoPointer(caps.MinSeconds)
		}
		for _, pair := range [][2]**int{{&result.MaxPromptChars, &caps.MaxPromptChars}, {&result.MaxSeconds, &caps.MaxSeconds}, {&result.MaxImages, &caps.MaxImages}, {&result.MaxVideos, &caps.MaxVideos}, {&result.MaxAudios, &caps.MaxAudios}} {
			if *pair[1] != nil && (*pair[0] == nil || **pair[1] < **pair[0]) {
				*pair[0] = copyVideoPointer(*pair[1])
			}
		}
		if result.RequiredImages != nil && caps.RequiredImages != nil && *result.RequiredImages != *caps.RequiredImages {
			return nil
		}
		if caps.RequiredImages != nil {
			result.RequiredImages = copyVideoPointer(caps.RequiredImages)
		}
		frames := result.Frames != nil && *result.Frames && caps.Frames != nil && *caps.Frames
		result.Frames = &frames
		required := (result.ReferenceDurationRequired != nil && *result.ReferenceDurationRequired) || (caps.ReferenceDurationRequired != nil && *caps.ReferenceDurationRequired)
		result.ReferenceDurationRequired = &required
		if result.SecondsSteps == nil {
			result.SecondsSteps = slices.Clone(caps.SecondsSteps)
		} else if caps.SecondsSteps != nil {
			result.SecondsSteps = slices.DeleteFunc(result.SecondsSteps, func(value int) bool { return !slices.Contains(caps.SecondsSteps, value) })
			if len(result.SecondsSteps) == 0 {
				return nil
			}
		}
	}
	if result == nil {
		return nil
	}
	if result.MinSeconds != nil && result.MaxSeconds != nil && *result.MinSeconds > *result.MaxSeconds {
		return nil
	}
	if result.RequiredImages != nil && result.MaxImages != nil && *result.RequiredImages > *result.MaxImages {
		return nil
	}
	if result.SecondsSteps != nil {
		result.SecondsSteps = slices.DeleteFunc(result.SecondsSteps, func(value int) bool {
			return (result.MinSeconds != nil && value < *result.MinSeconds) || (result.MaxSeconds != nil && value > *result.MaxSeconds)
		})
		if len(result.SecondsSteps) == 0 {
			return nil
		}
	}
	return result
}
