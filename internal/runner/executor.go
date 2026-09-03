package runner

import agentexecutor "github.com/DeveloperDurp/durpdeploy-agent/executor"

type Executor = agentexecutor.Executor
type Step = agentexecutor.Step
type ExecutionConfig = agentexecutor.ExecutionConfig
type Job = agentexecutor.Job
type JobConfig = agentexecutor.JobConfig
type Callbacks = agentexecutor.Callbacks
type CallbacksConfig = agentexecutor.CallbacksConfig

var ErrCancelled = agentexecutor.ErrCancelled

var NewExecutor = agentexecutor.NewExecutor
var NewJob = agentexecutor.NewJob
var NewCallbacks = agentexecutor.NewCallbacks
