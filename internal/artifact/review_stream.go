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
	err := s.array(func() error {
		resource, count, bad, err := s.resource(render != nil)
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
		change, err := render(resource)
		if err == nil {
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
				return s.decode(&resource.Address)
			}
		case "change":
			return s.object(func(key string) error {
				if reviewField(key, "actions") == "actions" {
					var err error
					resource.Actions, counts, invalid, err = s.actions(details)
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

func (s *reviewJSON) actions(details bool) ([]string, GateReview, bool, error) {
	var actions []string
	if err := s.space(); err != nil {
		return nil, GateReview{}, false, err
	}
	if b, _ := s.peek(); details && b == '[' {
		actions = make([]string, 0)
	}
	var counts GateReview
	var invalid bool
	err := s.array(func() error {
		var action string
		if err := s.version(&action); err != nil {
			return err
		}
		switch action {
		case "create":
			counts.Create++
		case "update":
			counts.Update++
		case "delete":
			counts.Delete++
		case "read":
			counts.Read++
		case "no-op":
		default:
			invalid = true
		}
		if details {
			actions = append(actions, action)
		}
		return nil
	})
	return actions, counts, invalid, err
}

func (s *reviewJSON) resourceValue(
	resource *terraformResource,
	key string,
) error {
	switch reviewField(key, "before", "after", "before_sensitive", "after_sensitive", "after_unknown") {
	case "before":
		return s.decode(&resource.Before)
	case "after":
		return s.decode(&resource.After)
	case "before_sensitive":
		return s.decode(&resource.BeforeSensitive)
	case "after_sensitive":
		return s.decode(&resource.AfterSensitive)
	case "after_unknown":
		return s.decode(&resource.AfterUnknown)
	default:
		return s.skip()
	}
}
