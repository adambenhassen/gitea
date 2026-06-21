// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"

	"xorm.io/xorm"
)

type actionTaskWithLogLineSteps struct {
	LogLineSteps []byte `xorm:"LONGBLOB"`
}

func (actionTaskWithLogLineSteps) TableName() string {
	return "action_task"
}

func AddLogLineStepsToActionTask(x db.EngineMigration) error {
	_, err := x.SyncWithOptions(xorm.SyncOptions{
		IgnoreDropIndices: true,
	}, new(actionTaskWithLogLineSteps))
	return err
}
