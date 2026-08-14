package mfa

import "errors"

func (s *FactorStore) BeginTOTPEnrollment(
	accountName string,
) (TOTPEnrollment, string, error) {
	if s.box == nil {
		return TOTPEnrollment{}, "", ErrMFAFactorOperation
	}
	enrollment, err := s.totp.Enroll("DurpDeploy", accountName)
	if err != nil {
		return TOTPEnrollment{}, "", ErrMFAFactorOperation
	}
	encryptedSeed, err := s.box.Encrypt(enrollment.Seed)
	if err != nil {
		return TOTPEnrollment{}, "", ErrMFAFactorOperation
	}
	return enrollment, encryptedSeed, nil
}

func (s *FactorStore) ConfirmTOTPEnrollment(
	encryptedSeed string,
	code string,
	lastAcceptedStep int64,
) (string, int64, error) {
	if s.box == nil {
		return "", 0, ErrMFAFactorOperation
	}
	seed, err := s.box.Decrypt(encryptedSeed)
	if err != nil {
		return "", 0, ErrMFAFactorOperation
	}
	step, err := s.totp.Verify(code, seed, lastAcceptedStep)
	if err != nil {
		if errors.Is(err, ErrInvalidTOTP) || errors.Is(err, ErrTOTPReplay) {
			return "", 0, err
		}
		return "", 0, ErrMFAFactorOperation
	}
	return seed, step, nil
}
