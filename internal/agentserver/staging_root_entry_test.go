package agentserver_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestAgentStagingRejectsFileRootBeforePublication(t *testing.T) {
	f := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, f)
	seedStageInput(t, f, id)
	var identity struct {
		Token string `json:"claim_token"`
	}
	if err := json.Unmarshal([]byte(claim), &identity); err != nil {
		t.Fatal(err)
	}
	response := postAgent(t, f,
		fmt.Sprintf("/agent/v1/deployments/%d/start", id), claim)
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	for _, name := range []string{".", "./."} {
		for _, data := range []string{"", "invalid-root"} {
			stageRequest(t, f, id, identity.Token, "upload",
				stageArchive(t, map[string]string{name: data}), 422)
		}
	}
	stageRequest(t, f, id, identity.Token, "upload",
		stageArchive(t, map[string]string{"valid": "published"}), 204)
}
