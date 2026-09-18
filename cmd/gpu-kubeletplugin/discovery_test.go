/*
Copyright (c) Advanced Micro Devices, Inc. All rights reserved.

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

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ROCm/k8s-gpu-dra-driver/pkg/amdgpu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/dynamic-resource-allocation/deviceattribute"
)

func TestNUMAAttributeForm(t *testing.T) {
	assert.Equal(t, deviceattribute.ScalarAttribute, numaAttributeForm(false))
	assert.Equal(t, deviceattribute.ListAttribute, numaAttributeForm(true))
}

func TestGetMemoryBytes(t *testing.T) {
	withVram := map[string]interface{}{"vramBytes": uint64(16 * 1024 * 1024 * 1024)}
	assert.Equal(t, uint64(16*1024*1024*1024), getMemoryBytes(withVram, "device", "0000:00:00.0"))

	// Unreadable VRAM reports 0 instead of a fabricated capacity.
	assert.Equal(t, uint64(0), getMemoryBytes(map[string]interface{}{}, "device", "0000:00:00.0"))
	assert.Equal(t, uint64(0), getMemoryBytes(map[string]interface{}{"vramBytes": uint64(0)}, "partition", "0000:00:00.0"))
}

func TestGetVFIOParentInfo(t *testing.T) {
	root := t.TempDir()
	amdgpu.SetSysfsRoot(root)
	t.Cleanup(amdgpu.ResetSysfsRoot)

	pciRoot := filepath.Join(root, "sys/bus/pci/devices")
	pfAddr := "0000:0a:00.0"
	vfAddr := "0000:0b:00.0"
	pfPath := filepath.Join(pciRoot, pfAddr)
	vfPath := filepath.Join(pciRoot, vfAddr)
	require.NoError(t, os.MkdirAll(pfPath, 0755))
	require.NoError(t, os.MkdirAll(vfPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pfPath, "sriov_totalvfs"), []byte("8\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(pfPath, "sriov_numvfs"), []byte("4\n"), 0644))
	require.NoError(t, os.Symlink(filepath.Join("..", pfAddr), filepath.Join(vfPath, "physfn")))

	isVF, parent, total, active := getVFIOParentInfo(vfAddr)
	assert.True(t, isVF)
	assert.Equal(t, pfAddr, parent)
	assert.Equal(t, 8, total)
	assert.Equal(t, 4, active)
}

func TestGetVFIOParentInfoForPF(t *testing.T) {
	root := t.TempDir()
	amdgpu.SetSysfsRoot(root)
	t.Cleanup(amdgpu.ResetSysfsRoot)

	pciRoot := filepath.Join(root, "sys/bus/pci/devices")
	pfAddr := "0000:0a:00.0"
	pfPath := filepath.Join(pciRoot, pfAddr)
	require.NoError(t, os.MkdirAll(pfPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pfPath, "sriov_totalvfs"), []byte("8\n"), 0644))

	isVF, parent, total, active := getVFIOParentInfo(pfAddr)
	assert.False(t, isVF)
	assert.Equal(t, pfAddr, parent)
	assert.Equal(t, 8, total)
	assert.Equal(t, 0, active)
}
