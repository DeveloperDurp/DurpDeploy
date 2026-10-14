package artifact

import (
	"strings"
)

func reviewField(key string, names ...string) string {
	for _, name := range names {
		if strings.EqualFold(key, name) {
			return name
		}
	}
	return ""
}

func (s *reviewJSON) version(target *string) error {
	if err := s.space(); err != nil {
		return err
	}
	if b, _ := s.peek(); b == 'n' {
		return s.literal("null")
	}
	value, _, err := s.stringToken(256)
	if err == nil {
		*target = value
	}
	return err
}

func (s *reviewJSON) terraform(
	render func(terraformResource) (TerraformResourceChange, error),
) (GateReview, []TerraformResourceChange, error) {
	var counts GateReview
	var changes []TerraformResourceChange
	if render != nil {
		changes = make([]TerraformResourceChange, 0)
	}
	var version string
	var invalid bool
	err := s.object(func(key string) error {
		switch reviewField(key, "format_version", "resource_changes") {
		case "format_version":
			return s.version(&version)
		case "resource_changes":
			var err error
			counts, changes, invalid, err = s.resources(render)
			return err
		default:
			return s.skip()
		}
	})
	if err == nil && (version == "" || invalid ||
		(render != nil && !strings.HasPrefix(version, "1."))) {
		err = ErrGateConfig
	}
	return counts, changes, err
}

func (s *reviewJSON) resources(
	render func(terraformResource) (TerraformResourceChange, error),
) (GateReview, []TerraformResourceChange, bool, error) {
	var counts GateReview
	var changes []TerraformResourceChange
	if render != nil {
		changes = make([]TerraformResourceChange, 0)
	}
	var invalid bool
	if s.preview != nil {
		*s.preview = terraformPreview{}
	}
	err := s.array(func() error {
		details := render != nil && s.preview.canRetain(len(changes))
		resource, count, bad, err := s.resource(details)
		if err != nil {
			return err
		}
		counts.Create += count.Create
		counts.Update += count.Update
		counts.Delete += count.Delete
		counts.Read += count.Read
		invalid = invalid || bad
		if render == nil {
			return nil
		}
		if !details {
			s.preview.omitted++
			s.preview.truncated = true
			return nil
		}
		change, err := render(resource)
		if err == nil && s.preview.retain(change) {
			changes = append(changes, change)
		}
		return err
	})
	return counts, changes, invalid, err
}

type terraformResource struct {
	Address                                                      string
	Actions                                                      []string
	Before, After, BeforeSensitive, AfterSensitive, AfterUnknown any
	omitted                                                      map[string]bool
}

func (s *reviewJSON) resource(
	details bool,
) (terraformResource, GateReview, bool, error) {
	var resource terraformResource
	var counts GateReview
	var invalid bool
	err := s.object(func(key string) error {
		switch reviewField(key, "address", "change") {
		case "address":
			if details {
				return s.resourceAddress(&resource)
			}
		case "change":
			return s.object(func(key string) error {
				if reviewField(key, "actions") == "actions" {
					actions, err := s.actions(details)
					resource.Actions, counts, invalid = actions.list, actions.counts, !actions.valid
					resource.markOmitted("actions", actions.omitted)
					return err
				}
				if details {
					return s.resourceValue(&resource, key)
				}
				return s.skip()
			})
		}
		return s.skip()
	})
	return resource, counts, invalid, err
}

func (s *reviewJSON) resourceValue(
	resource *terraformResource,
	key string,
) error {
	field := reviewField(
		key,
		"before",
		"after",
		"before_sensitive",
		"after_sensitive",
		"after_unknown",
	)
	var target *any
	switch field {
	case "before":
		target = &resource.Before
	case "after":
		target = &resource.After
	case "before_sensitive":
		target = &resource.BeforeSensitive
	case "after_sensitive":
		target = &resource.AfterSensitive
	case "after_unknown":
		target = &resource.AfterUnknown
	default:
		return s.skip()
	}
	*target = nil
	omitted, err := s.decodePreview(target, previewValueBytes)
	resource.markOmitted(field, omitted)
	return err
}

func (s *reviewJSON) resourceAddress(resource *terraformResource) error {
	if err := s.space(); err != nil {
		return err
	}
	if b, _ := s.peek(); b == 'n' {
		return s.literal("null")
	}
	if b, _ := s.peek(); b != '"' {
		return ErrGateConfig
	}
	omitted, err := s.decodePreview(&resource.Address, previewAddressBytes)
	resource.markOmitted("address", omitted)
	if omitted {
		resource.Address = terraformOmitted
	}
	return err
}
