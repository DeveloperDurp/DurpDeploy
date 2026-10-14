package artifact

func (s *reviewJSON) object(member func(string) error) error {
	return s.container('{', '}', func() error { return s.member(member) })
}

func (s *reviewJSON) array(item func() error) error {
	return s.container('[', ']', item)
}

func (s *reviewJSON) container(start, end byte, item func() error) error {
	if err := s.space(); err != nil {
		return err
	}
	if b, _ := s.peek(); b == 'n' {
		return s.literal("null")
	}
	if err := s.expect(start); err != nil {
		return err
	}
	if err := s.enter(); err != nil {
		return err
	}
	defer func() { s.depth-- }()
	if err := s.space(); err != nil {
		return err
	}
	if b, _ := s.peek(); b == end {
		return s.expect(end)
	}
	for {
		if err := item(); err != nil {
			return err
		}
		done, err := s.separator(end)
		if err != nil || done {
			return err
		}
	}
}

func (s *reviewJSON) separator(end byte) (bool, error) {
	if err := s.space(); err != nil {
		return false, err
	}
	b, err := s.read()
	if err != nil {
		return false, err
	}
	if b == end {
		return true, nil
	}
	if b != ',' {
		return false, ErrGateConfig
	}
	return false, s.space()
}

func (s *reviewJSON) member(visit func(string) error) error {
	limit := 0
	if visit != nil {
		limit = 256
	}
	key, truncated, err := s.stringToken(limit)
	if err != nil {
		return err
	}
	if err := s.space(); err != nil {
		return err
	}
	if err := s.expect(':'); err != nil {
		return err
	}
	if visit == nil {
		return s.skip()
	}
	if truncated {
		key = ""
	}
	return visit(key)
}
