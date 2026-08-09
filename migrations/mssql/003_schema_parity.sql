-- +goose Up

ALTER TABLE deployment_approvals ADD CONSTRAINT fk_deployment_approvals_approver_user
    FOREIGN KEY (approver_user_id) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE notification_events ADD CONSTRAINT fk_notification_events_environment
    FOREIGN KEY (environment_id) REFERENCES environments(id) ON DELETE SET NULL;
ALTER TABLE audit_log ADD CONSTRAINT fk_audit_log_user
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE deployment_approvals ADD CONSTRAINT df_deployment_approvals_role
    DEFAULT 'admin' FOR required_approver_role;
ALTER TABLE notification_events ADD CONSTRAINT df_notification_events_results
    DEFAULT '{}' FOR results;

ALTER TABLE api_tokens ALTER COLUMN token_hash NVARCHAR(255) NOT NULL;
ALTER TABLE api_tokens ADD CONSTRAINT uq_api_tokens_token_hash UNIQUE (token_hash);

CREATE INDEX idx_steps_project_id ON steps(project_id);
CREATE INDEX idx_variables_project_id ON variables(project_id);
CREATE INDEX idx_releases_project_id ON releases(project_id);
CREATE INDEX idx_deployments_release_id ON deployments(release_id);
CREATE INDEX idx_deployments_environment_id ON deployments(environment_id);
CREATE INDEX idx_deployment_logs_deployment_id ON deployment_logs(deployment_id);
CREATE INDEX idx_lifecycle_stages_lifecycle_id ON lifecycle_stages(lifecycle_id);
CREATE INDEX idx_scheduled_deployments_due ON scheduled_deployments(next_run_at)
    WHERE enabled = 1;
CREATE INDEX idx_step_template_versions_template_id
    ON step_template_versions(template_id);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
CREATE INDEX idx_audit_log_user_id ON audit_log(user_id);
CREATE INDEX idx_audit_log_created_at ON audit_log(created_at DESC);
CREATE INDEX idx_audit_log_action ON audit_log(action);
CREATE INDEX idx_project_members_user_id ON project_members(user_id);
CREATE INDEX idx_deployment_approvals_deployment_id
    ON deployment_approvals(deployment_id);
CREATE INDEX idx_notification_events_created_at
    ON notification_events(created_at DESC);
CREATE INDEX idx_notification_events_project_id ON notification_events(project_id);
CREATE INDEX idx_api_tokens_user_id ON api_tokens(user_id);
CREATE INDEX idx_api_tokens_revoked_at ON api_tokens(revoked_at);
