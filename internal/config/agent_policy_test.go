package config_test

import (
	"reflect"
	"testing"

	"durpdeploy/internal/config"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/stretchr/testify/require"
)

func TestAgentPolicyProjection(t *testing.T) {
	limits := config.Defaults().Limits
	require.Equal(t, resourcepolicy.Defaults(), limits.AgentPolicy())
	for _, group := range []string{
		"Execution", "Artifact", "Staging", "Container", "Transfer",
	} {
		fields := reflect.ValueOf(&limits).Elem().FieldByName(group)
		for index := 0; index < fields.NumField(); index++ {
			name := fields.Type().Field(index).Name
			if group == "Transfer" && name != "IdleTimeoutSeconds" {
				continue
			}
			t.Run(group+"/"+name, func(t *testing.T) {
				changed := limits
				field := reflect.ValueOf(&changed).Elem().
					FieldByName(group).FieldByName(name)
				if field.Kind() == reflect.Float64 {
					field.SetFloat(field.Float() + 1)
				} else {
					field.SetInt(field.Int() + 1)
				}
				got := reflect.ValueOf(changed.AgentPolicy()).
					FieldByName(group).FieldByName(name)
				require.Equal(t, field.Interface(), got.Interface())
				require.NotEqual(t, limits.AgentPolicy(), changed.AgentPolicy())
			})
		}
	}
	limits.Merge.AgentOutputs++
	limits.Transfer.Global++
	limits.Transfer.PerAgent++
	limits.HTTP.JSONBytes++
	limits.Logs.PageRows++
	limits.TokenTelemetry.Concurrency++
	require.Equal(t, resourcepolicy.Defaults(), limits.AgentPolicy())
}
