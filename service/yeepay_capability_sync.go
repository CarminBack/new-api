package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

// Marked generated sections are replaced, never stacked. The persisted plugin
// is the protocol implementation; upstream JSON is data and never JS source.
const yeepaySyncStart = "// BEGIN YEEPAY CATALOG SYNC\n"
const yeepaySyncEnd = "// END YEEPAY CATALOG SYNC\n"

func buildYeepaySyncedPlugin(source string, catalog []yeepayCatalogModel, request YeepaySyncRequest) (*model.TaskPlugin, *jsplugin.LoadedPlugin, []AistarsLabSeedanceModel, []string, error) {
	incumbent, originalErr := jsplugin.NewRegistry().Register(source, jsplugin.Options{})
	if originalErr != nil {
		return nil, nil, nil, nil, originalErr
	}
	if len(catalog) == 0 {
		return nil, nil, nil, nil, errors.New("empty Yeepay catalog")
	}
	// Keep legacy declarations for pinned historical tasks and retired models.
	start := strings.Index(source, "const ROWS = ")
	if start < 0 {
		return nil, nil, nil, nil, errors.New("Yeepay plugin template changed; manual adaptation is required")
	}
	end := strings.Index(source[start:], ";")
	if end < 0 {
		return nil, nil, nil, nil, errors.New("invalid Yeepay row table")
	}
	end += start
	var rows [][]any
	if err := common.UnmarshalJsonStr(source[start+len("const ROWS = "):end], &rows); err != nil {
		return nil, nil, nil, nil, errors.New("Yeepay plugin row table is not plain JSON; manual review is required")
	}
	indexes := map[string]int{}
	for i, r := range rows {
		if len(r) != 10 {
			return nil, nil, nil, nil, errors.New("Yeepay plugin row layout changed")
		}
		name, ok := r[0].(string)
		if !ok {
			return nil, nil, nil, nil, errors.New("invalid stored model row")
		}
		indexes[name] = i
	}
	capsMap := map[string]jsplugin.VideoCapabilities{}
	var warnings []string
	var models []AistarsLabSeedanceModel
	seen := map[string]bool{}
	slices.SortFunc(catalog, func(a, b yeepayCatalogModel) int { return strings.Compare(a.ID, b.ID) })
	for _, item := range catalog {
		if item.CapabilityType != "video" {
			continue
		}
		name := item.ID
		if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, ",\r\n") || seen[name] {
			return nil, nil, nil, nil, errors.New("invalid or duplicate Yeepay model name")
		}
		seen[name] = true
		c, p := item.Capabilities, item.Pricing
		if p.Currency != "credits" || (p.Mode != "per_request" && p.Mode != "per_generated_second") {
			return nil, nil, nil, nil, fmt.Errorf("%s: unsupported pricing currency or mode; manual adaptation required", name)
		}
		for _, v := range []*float64{p.Tokens, p.VideoTokens, p.Reference} {
			if v != nil && *v != 0 {
				return nil, nil, nil, nil, fmt.Errorf("%s: token/reference-second pricing needs manual adaptation", name)
			}
		}
		if c.MinSeconds == nil || c.MaxSeconds == nil || *c.MinSeconds < 1 || *c.MaxSeconds > constant.MaxTaskDurationSeconds || *c.MinSeconds > *c.MaxSeconds || len(c.DurationOptions) == 0 {
			return nil, nil, nil, nil, fmt.Errorf("%s: missing duration constraints", name)
		}
		steps := slices.Clone(c.DurationOptions)
		slices.Sort(steps)
		if len(slices.Compact(slices.Clone(steps))) != len(steps) || steps[0] != *c.MinSeconds || steps[len(steps)-1] != *c.MaxSeconds {
			return nil, nil, nil, nil, fmt.Errorf("%s: inconsistent duration options", name)
		}
		caps := jsplugin.VideoCapabilities{RequestFormat: "unified_video_v1", MinSeconds: c.MinSeconds, MaxSeconds: c.MaxSeconds, SecondsSteps: steps, Resolutions: slices.Clone(c.Resolutions)}
		for i, resolution := range caps.Resolutions {
			if strings.EqualFold(resolution, "2k") || strings.EqualFold(resolution, "4k") {
				caps.Resolutions[i] = strings.ToUpper(resolution)
			} else {
				caps.Resolutions[i] = strings.ToLower(resolution)
			}
		}
		for _, r := range c.Ratios {
			if yeepayRatioPattern.MatchString(r) {
				caps.Ratios = append(caps.Ratios, r)
			} else {
				warnings = append(warnings, name+": auto/adaptive or unknown aspect ratio omitted")
			}
		}
		for _, pair := range []struct {
			typ string
			raw *int
			dst **int
		}{{"image", c.MaxImages, &caps.MaxImages}, {"audio", c.MaxAudios, &caps.MaxAudios}, {"video", c.MaxVideos, &caps.MaxVideos}} {
			supported, ok := c.Support[pair.typ]
			if !ok {
				return nil, nil, nil, nil, fmt.Errorf("%s: missing reference support flag", name)
			}
			if !supported {
				zero := 0
				*pair.dst = &zero
				continue
			}
			if pair.raw == nil || *pair.raw == 99999 {
				warnings = append(warnings, name+": "+pair.typ+" reference maximum is unknown")
				continue
			}
			if *pair.raw < 0 {
				return nil, nil, nil, nil, fmt.Errorf("%s: invalid reference maximum", name)
			}
			*pair.dst = pair.raw
		}
		frames := slices.Contains(c.Materials, "first_frame") && slices.Contains(c.Materials, "last_frame")
		if slices.Contains(c.Materials, "first_frame") != slices.Contains(c.Materials, "last_frame") {
			return nil, nil, nil, nil, fmt.Errorf("%s: first-frame-only mode requires manual adaptation", name)
		}
		caps.Frames = &frames
		if len(c.VideoModes) > 0 {
			warnings = append(warnings, name+": only ordinary reference video is adapted; edit/extend are not enabled")
		}
		capsMap[name] = caps
		unit := "request"
		rawPrice := p.Base
		fact := "videos"
		if p.Mode == "per_generated_second" {
			unit = "second"
			rawPrice = p.Seconds
			fact = "seconds"
		}
		price, err := yeepaySalePrice(rawPrice, request)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		expression := fmt.Sprintf(`tier("base", u("%s") * %s)`, fact, price)
		if unit == "second" && p.WithVideo != nil && *p.WithVideo != 0 {
			withPrice, err := yeepaySalePrice(p.WithVideo, request)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("%s: %w", name, err)
			}
			expression = fmt.Sprintf(`u("video_input") == "video" ? tier("with_video", u("seconds") * %s) : %s`, withPrice, expression)
		}
		row := []any{name, caps.Resolutions, *caps.MinSeconds, *caps.MaxSeconds, steps, caps.MaxImages, caps.MaxAudios, caps.MaxVideos, unit, false}
		if i, ok := indexes[name]; ok {
			rows[i] = row
		} else {
			indexes[name] = len(rows)
			rows = append(rows, row)
		}
		numericPrice, _ := strconv.ParseFloat(price, 64)
		models = append(models, AistarsLabSeedanceModel{Price: numericPrice, PublicModel: name, UpstreamModel: name, BillingUnit: p.Mode, BillingExpression: expression, DurationMin: caps.MinSeconds, DurationMax: caps.MaxSeconds, DurationOptions: steps})
	}
	if len(models) == 0 {
		return nil, nil, nil, nil, errors.New("no supported Yeepay video models")
	}
	encoded, err := common.Marshal(rows)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	source = source[:start] + "const ROWS = " + string(encoded) + source[end:]
	// Remove our previous generated sections before regenerating. Unmarked manual
	// protocol edits stay present and are checked by the template anchors/compiler.
	for strings.Contains(source, yeepaySyncStart) {
		a := strings.Index(source, yeepaySyncStart)
		b := strings.Index(source[a:], yeepaySyncEnd)
		if b < 0 {
			return nil, nil, nil, nil, errors.New("incomplete generated Yeepay section")
		}
		source = source[:a] + source[a+b+len(yeepaySyncEnd):]
	}
	// Preserve metadata for retired models that were generated by this synchronizer.
	// Active catalog entries are rebuilt from the authenticated directory.
	for name, c := range incumbent.Meta.VideoCapabilities {
		if _, ok := capsMap[name]; !ok {
			capsMap[name] = c
		}
	}
	capsJSON, err := common.Marshal(capsMap)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	anchor := "const UNIT_PRICE = "
	if strings.Count(source, anchor) != 1 || strings.Count(source, "  if (references.length) body.references = references;") != 1 {
		return nil, nil, nil, nil, errors.New("Yeepay plugin template changed; review manual edits")
	}
	source = strings.Replace(source, anchor, yeepaySyncStart+"const YEEPAY_SYNC_CAPS = "+string(capsJSON)+";\nObject.assign(CAPABILITIES, YEEPAY_SYNC_CAPS);\n"+yeepaySyncEnd+anchor, 1)
	validation := `  const caps = YEEPAY_SYNC_CAPS[body.model];
  if (caps) {
    if (body.duration !== undefined && caps.secondsSteps && !caps.secondsSteps.includes(body.duration)) throw new Error("duration is not supported by this model");
    if (body.resolution && caps.resolutions && !caps.resolutions.some(function (v) { return v.toLowerCase() === body.resolution.toLowerCase(); })) throw new Error("resolution is not supported by this model");
    if (body.ratio && caps.ratios && !caps.ratios.includes(body.ratio)) throw new Error("aspect ratio is not supported by this model");
    if ((first || last) && caps.frames === false) throw new Error("frame references are not supported by this model");
    for (const pair of [["image", "maxImages"], ["audio", "maxAudios"], ["video", "maxVideos"]]) {
      if (caps[pair[1]] !== undefined && references.filter(function (ref) { return ref.type === pair[0]; }).length > caps[pair[1]]) throw new Error(pair[0] + " reference limit exceeded");
    }
    if (req.omni_reference_task_type !== undefined) throw new Error("explicit reference edit/extend modes are not adapted");
  }
`
	source = strings.Replace(source, "  if (references.length) body.references = references;", yeepaySyncStart+validation+yeepaySyncEnd+"  if (references.length) body.references = references;", 1)
	nullFix := "  for (const field of [\"maxImages\", \"maxAudios\", \"maxVideos\"]) if (caps[field] === null) delete caps[field];\n"
	if !strings.Contains(source, nullFix) {
		source = strings.Replace(source, "  if (row[1]) caps.resolutions = row[1];", nullFix+"  if (row[1]) caps.resolutions = row[1];", 1)
	}
	// Empty usage groups are invalid manifests. Existing templates may already
	// filter them; never duplicate filters or alter their nonempty definitions.
	source = strings.Replace(source, "  ],\n  protocols:", "  ].filter(function (profile) { return profile.models.length > 0; }),\n  protocols:", 1)
	versionPattern := regexp.MustCompile(`version: "[^"]+"`)
	if len(versionPattern.FindAllString(source, -1)) != 1 {
		return nil, nil, nil, nil, errors.New("ambiguous plugin version")
	}
	source = versionPattern.ReplaceAllString(source, `version: "1.0.3"`)
	digest := sha256.Sum256([]byte(source))
	version := fmt.Sprintf("1.0.3+sync.%x", digest[:12])
	source = versionPattern.ReplaceAllString(source, `version: "`+version+`"`)
	loaded, err := jsplugin.NewRegistry().Register(source, jsplugin.Options{})
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("generated Yeepay plugin rejected: %w", err)
	}
	for _, item := range models {
		schema, _ := loaded.Meta.UsageForModel(item.PublicModel)
		if err = billing_setting.SmokeTestTaskExpr(item.BillingExpression, schema); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("%s: invalid generated price expression", item.PublicModel)
		}
	}
	if err = jsplugin.PreflightRoutingConflict(jsplugin.DefaultRegistry.Generation(), loaded); err != nil {
		return nil, nil, nil, nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	return &model.TaskPlugin{Key: "yeepay", APIVersion: 1, Version: version, Source: model.LongText(source), SourceHash: hash, Enabled: true, Remark: "Yeepay upstream catalog synchronization"}, loaded, models, warnings, nil
}
