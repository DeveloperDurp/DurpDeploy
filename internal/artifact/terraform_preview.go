package artifact

import (
	"encoding/json"

	"durpdeploy/internal/logscrub"
)

const (
	previewValueBytes   = 64 << 10
	previewAddressBytes = 4 << 10
	previewResources    = 100
	previewTextBytes    = 1 << 20
	terraformOmitted    = "[OMITTED FROM PREVIEW]"
)

type terraformPreview struct {
	used      int
	omitted   int64
	truncated bool
}

func (p *terraformPreview) canRetain(count int) bool {
	return count < previewResources && p.used < previewTextBytes
}

func (p *terraformPreview) retain(change TerraformResourceChange) bool {
	size := len(change.Address) + len(change.Before) + len(change.After)
	if size > previewTextBytes-p.used {
		p.omitted++
		p.truncated = true
		return false
	}
	p.used += size
	p.truncated = p.truncated || len(change.OmittedFields) > 0
	return true
}

func (r *terraformResource) markOmitted(field string, omitted bool) {
	if !omitted {
		delete(r.omitted, field)
		return
	}
	if r.omitted == nil {
		r.omitted = make(map[string]bool)
	}
	r.omitted[field] = true
}

func renderTerraformResource(
	r terraformResource,
	scrubber *logscrub.Scrubber,
) (TerraformResourceChange, error) {
	change := TerraformResourceChange{
		Address: scrubber.Scrub(r.Address),
		Actions: r.Actions,
	}
	before := terraformSide{value: r.Before, sensitive: r.BeforeSensitive,
		omitted: r.omitted["before"] || r.omitted["before_sensitive"]}
	after := terraformSide{
		value:     r.After,
		sensitive: r.AfterSensitive,
		unknown:   r.AfterUnknown,
		omitted: r.omitted["after"] || r.omitted["after_sensitive"] ||
			r.omitted["after_unknown"],
	}
	var err error
	change.Before, err = before.render(scrubber)
	if err != nil {
		return change, err
	}
	change.After, err = after.render(scrubber)
	if err != nil {
		return change, err
	}
	for _, field := range []string{"address", "actions", "before", "after"} {
		if r.omitted[field] ||
			(field == "before" && change.Before == terraformOmitted) ||
			(field == "after" && change.After == terraformOmitted) {
			change.OmittedFields = append(change.OmittedFields, field)
		}
	}
	return change, nil
}

type terraformSide struct {
	value, sensitive, unknown any
	omitted                   bool
}

func (s terraformSide) render(scrubber *logscrub.Scrubber) (string, error) {
	if s.omitted {
		return terraformOmitted, nil
	}
	raw, err := json.MarshalIndent(
		scrubTerraformValue(
			terraformValue(s.value, s.sensitive, s.unknown),
			scrubber,
		),
		"",
		"  ",
	)
	if err != nil {
		return "", ErrGateConfig
	}
	if len(raw) > previewValueBytes {
		return terraformOmitted, nil
	}
	return string(raw), nil
}
