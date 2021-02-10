/*
Copyright 2019 Intuit, Inc..
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/go-logr/logr"
	"github.com/keikoproj/upgrade-manager/api/v1alpha1"
)

const (
	ShellBinary = "/bin/sh"
)

type ScriptRunner struct {
	logr.Logger
}

type ScriptTarget struct {
	InstanceID    string
	NodeName      string
	UpgradeObject *v1alpha1.RollingUpgrade
}

func NewScriptRunner(logger logr.Logger) ScriptRunner {
	return ScriptRunner{
		Logger: logger,
	}
}

func (r *ScriptRunner) getEnv(target ScriptTarget) []string {
	var (
		asgNameEnv      = "ASG_NAME"
		instanceIdEnv   = "INSTANCE_ID"
		instanceNameEnv = "INSTANCE_NAME"
	)
	return []string{
		fmt.Sprintf("%s=%s", asgNameEnv, target.UpgradeObject.ScalingGroupName()),
		fmt.Sprintf("%s=%s", instanceIdEnv, target.InstanceID),
		fmt.Sprintf("%s=%s", instanceNameEnv, target.NodeName),
	}
}

func (r *ScriptRunner) runScript(script string, target ScriptTarget) (string, error) {
	r.Info("running script", "script", script, "name", target.UpgradeObject.NamespacedName())
	command := exec.Command(ShellBinary, "-c", script)
	command.Env = append(os.Environ(), r.getEnv(target)...)

	out, err := command.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func (r *ScriptRunner) PostTerminate(target ScriptTarget) error {
	script := target.UpgradeObject.PostTerminateScript()
	if script == "" {
		return nil
	}

	out, err := r.runScript(script, target)
	if err != nil {
		r.Info("script execution failed", "output", out, "stage", "PostTerminate", "script", script, "name", target.UpgradeObject.NamespacedName(), "target", target.NodeName)
		return err
	}

	r.Info("script execution succeeded", "output", out, "stage", "PostTerminate", "script", script, "name", target.UpgradeObject.NamespacedName(), "target", target.NodeName)

	return nil
}

func (r *ScriptRunner) PreDrain(target ScriptTarget) error {
	script := target.UpgradeObject.PreDrainScript()
	if script == "" {
		return nil
	}

	out, err := r.runScript(script, target)
	if err != nil {
		r.Info("script execution failed", "output", out, "stage", "PreDrain", "script", script, "name", target.UpgradeObject.NamespacedName(), "target", target.NodeName)
		return err
	}

	r.Info("script execution succeeded", "output", out, "stage", "PreDrain", "script", script, "name", target.UpgradeObject.NamespacedName(), "target", target.NodeName)
	return nil
}

func (r *ScriptRunner) PostDrain(target ScriptTarget) error {
	script := target.UpgradeObject.PostDrainScript()
	if script == "" {
		return nil
	}

	out, err := r.runScript(script, target)
	if err != nil {
		r.Info("script execution failed", "output", out, "stage", "PostDrain", "script", script, "name", target.UpgradeObject.NamespacedName(), "target", target.NodeName)
		return err
	}

	r.Info("script execution succeeded", "output", out, "stage", "PostDrain", "script", script, "name", target.UpgradeObject.NamespacedName(), "target", target.NodeName)
	return nil
}

func (r *ScriptRunner) PostWait(target ScriptTarget) error {
	script := target.UpgradeObject.PostWaitScript()
	if script == "" {
		return nil
	}

	out, err := r.runScript(script, target)
	if err != nil {
		r.Info("script execution failed", "output", out, "stage", "PostWait", "script", script, "name", target.UpgradeObject.NamespacedName(), "target", target.NodeName)
		return err
	}

	r.Info("script execution succeeded", "output", out, "stage", "PostWait", "script", script, "name", target.UpgradeObject.NamespacedName(), "target", target.NodeName)
	return nil
}
