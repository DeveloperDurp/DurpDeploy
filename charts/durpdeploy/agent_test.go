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
			var checks agentListenerChecks
			for _, resource := range renderChart(t, fixture.values) {
				switch resource.Kind {
				case "Service":
					checks.checkService(resource, fixture.port)
				case "Deployment":
					checks.checkDeployment(
						resource,
						fixture.port,
						fixture.address,
						fixture.endpoint,
					)
				}
			}
			if !checks.service || !checks.container || !checks.address ||
				!checks.endpoint {
				t.Fatalf(
					"service=%v container=%v address=%v endpoint=%v",
					checks.service,
					checks.container,
					checks.address,
					checks.endpoint,
				)
			}
		})
	}
}

type agentListenerChecks struct {
	service, container, address, endpoint bool
}

func (checks *agentListenerChecks) checkService(
	resource chartResource,
	wantPort int,
) {
	for _, port := range resource.Spec.Ports {
		if port.Name == "agent" {
			checks.service = port.Port == wantPort && port.TargetPort == "agent"
		}
	}
}

func (checks *agentListenerChecks) checkDeployment(
	resource chartResource,
	wantPort int,
	wantAddress, wantEndpoint string,
) {
	for _, item := range resource.Spec.Template.Spec.Containers {
		for _, port := range item.Ports {
			if port.Name == "agent" {
				checks.container = port.Port == wantPort
			}
		}
		for _, env := range item.Env {
			switch env.Name {
			case "DURPDEPLOY_AGENT_LISTEN_ADDR":
				checks.address = env.Value == wantAddress
			case "DURPDEPLOY_AGENT_PUBLIC_URL":
				checks.endpoint = env.Value == wantEndpoint
			}
		}
	}
}
