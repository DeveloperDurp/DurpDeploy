package artifact

import "testing"

func TestRepositoryValidateForSave(t *testing.T) {
	for _, host := range []string{
		"127.0.0.1", "127.0.0.1.", "LOCALHOST.", "service.localhost.",
		"2130706433", "0177.0.0.1", "0x7f.0.0.1", "127.1",
		"169.254.169.254", "100.100.100.200", "[fd00:ec2::254]",
		"[::ffff:127.0.0.1]", "[fe80::1%25eth0]",
	} {
		t.Run(host, func(t *testing.T) {
			source := Repository{
				URLTemplate: "https://" + host + "/{version}.zip",
				AuthType:    "noauth",
			}
			if err := source.ValidateForSave(); err == nil {
				t.Fatal("expected save to reject destination")
			}
		})
	}
	for _, host := range []string{
		"repo.example", "REPO.EXAMPLE.", "host.containers.internal",
		"10.0.0.1", "192.168.1.1", "100.100.100.199", "[fd00::1]",
		"8.8.8.8", "[2001:4860:4860::8888]", "2130706433.example",
	} {
		source := Repository{
			URLTemplate: "https://" + host + "/{version}.zip",
			AuthType:    "noauth",
		}
		if err := source.ValidateForSave(); err != nil {
			t.Errorf("valid save %q: %v", host, err)
		}
	}
}
