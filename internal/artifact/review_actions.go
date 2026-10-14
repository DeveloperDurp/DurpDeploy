package artifact

type terraformActions struct {
	list    []string
	counts  GateReview
	valid   bool
	omitted bool
}

func (s *reviewJSON) actions(details bool) (terraformActions, error) {
	result := terraformActions{valid: true}
	if err := s.space(); err != nil {
		return result, err
	}
	if b, _ := s.peek(); details && b == '[' {
		result.list = make([]string, 0)
	}
	err := s.array(func() error {
		var action string
		if err := s.version(&action); err != nil {
			return err
		}
		switch action {
		case "create":
			result.counts.Create++
		case "update":
			result.counts.Update++
		case "delete":
			result.counts.Delete++
		case "read":
			result.counts.Read++
		case "no-op":
		default:
			result.valid = false
		}
		if details {
			if len(result.list) < 16 {
				result.list = append(result.list, action)
			} else {
				result.omitted = true
			}
		}
		return nil
	})
	return result, err
}
