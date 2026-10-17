package chart_test

import "testing"

func TestAgentListenerPorts(t *testing.T) {
	for _, fixture := range []struct {
		name, values, address, endpoint string
		port                            int
	}{
		{"default", "", "0.0.0.0:23187",
			"https://connection-durpdeploy.default.svc:23187", 23187},
		{"override", "agent:\n  port: 24443\n" +
			"  listenAddress: 0.0.0.0:24443\n" +
			"  publicURL: https://agents.example.test:25443\n",
			"0.0.0.0:24443", "https://agents.example.test:25443", 24443},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var service, container, address, endpoint bool
			for _, resource := range renderChart(t, fixture.values) {
				if resource.Kind == "Service" {
					for _, port := range resource.Spec.Ports {
						if port.Name == "agent" {
							service = port.Port == fixture.port &&
								port.TargetPort == "agent"
						}
					}
				}
				if resource.Kind != "Deployment" {
					continue
				}
				for _, item := range resource.Spec.Template.Spec.Containers {
					for _, port := range item.Ports {
						if port.Name == "agent" {
							container = port.Port == fixture.port
						}
					}
					for _, env := range item.Env {
						switch env.Name {
						case "DURPDEPLOY_AGENT_LISTEN_ADDR":
							address = env.Value == fixture.address
						case "DURPDEPLOY_AGENT_PUBLIC_URL":
							endpoint = env.Value == fixture.endpoint
						}
					}
				}
			}
			if !service || !container || !address || !endpoint {
				t.Fatalf("service=%v container=%v address=%v endpoint=%v",
					service, container, address, endpoint)
			}
		})
	}
}
