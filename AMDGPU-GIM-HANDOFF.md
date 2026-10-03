# AMDGPU/GIM live-transition investigation — parked

Updated: 2026-10-03

## Status: parked

This investigation is parked, not resolved. It will not be picked back up
unless AMD states that the live transition is a supported configuration.
Reasons:

1. **It isn't a prerequisite for DRA/KubeVirt/VFIO testing after all.** The DRA
   driver never loads or unloads GIM; it only discovers whatever driver mode a
   PF is already in (`cmd/gpu-kubeletplugin/discovery.go:289-291` enumerates
   PFs already bound to `vfio-pci` and GIM SR-IOV VFs;
   `cmd/gpu-kubeletplugin/vfio_manager.go:85-92` refuses to touch a PF with
   active VFs). Boot-time driver-mode selection is sufficient for the
   downstream testing; a reboot-free cycle is a nice-to-have, not a blocker.
2. **AMD does not document this as a supported transition.** The
   [Host Configuration](https://instinct.docs.amd.com/projects/virt-drv/en/latest/userguides/Host_configuration.html)
   guide requires blacklisting `amdgpu` at boot before GIM can take the
   devices; [Removing MxGPU](https://instinct.docs.amd.com/projects/virt-drv/en/latest/userguides/Removing_MxGPU.html)
   ends with reverting GRUB/BIOS settings — implying a reboot, not a live
   switch. There is no documented live-transition procedure in either
   direction.
3. **The investigation has reached concrete findings and filed them upstream.**
   Further progress depends on AMD, not on more local hardware time. See
   "Where this stands with AMD" below.

The rest of this document is the investigation record, corrected and updated
from the original findings.

## Objective (original)

Make the XE9785L host reliably cycle between the Fedora `amdgpu` driver and
AMD GIM without a host reboot, while preserving the expected SR-IOV behavior:

```text
amdgpu (8 PFs, 0 VFs)
  -> unload amdgpu
  -> load GIM (8 PFs, configured VFs)
  -> unload GIM
  -> load/rebind amdgpu (8 PFs, 0 VFs)
```

## Where this stands with AMD

- **[ROCm/amdgpu#234](https://github.com/ROCm/amdgpu/issues/234)** — originally
  filed for the reverse-direction panic (GIM unload → `amdgpu` probe →
  `amdgpu_in_reset()` NULL deref). Updated 2026-10-03 with the forward-direction
  failure below, which reframes the open question from "fix this one crash" to
  "is this transition supported at all, in either direction?" Still
  unanswered.
- **[amd/MxGPU-Virtualization#29](https://github.com/amd/MxGPU-Virtualization/issues/29)**
  — new issue filed 2026-10-03 for the forward-direction PSP-timeout finding
  (below), which lives entirely in stock GIM 9.2.0.K logic, not in any local
  patch.
- Local source changes are preserved on forks, unvalidated, and **not** opened
  as pull requests against the upstream repos:
  - GIM: [`johnahull/MxGPU-Virtualization` @ `mi355x-psp-handoff-investigation`](https://github.com/johnahull/MxGPU-Virtualization/tree/mi355x-psp-handoff-investigation)
  - amdgpu: [`johnahull/amdgpu` @ `fix/mi355x-vcn-teardown`](https://github.com/johnahull/amdgpu/tree/fix/mi355x-vcn-teardown)
  - `amd/MxGPU-Virtualization` is a release-drop mirror (12 commits total, all
    "Update for release X.Y.Z.K", no external code merges) — a PR there would
    not be reviewed. The amdgpu branch was written while chasing a moving
    target and was never hardware-validated; describing the fix in the issue
    and letting AMD evaluate it is more honest than opening an untested PR
    against GPU teardown code.

## Root cause findings (revised)

### Forward direction: GIM cannot initialize PSP after an `amdgpu` unload

This is the finding that changed the investigation's direction. It is **not**
caused by any local patch — it reproduces in stock GIM 9.2.0.K logic.

`mi300_psp_load_psp_fw()` (`libgv/core/hw/AI/mi300/mi300_psp.c`) waits for the
PSP bootloader-ready bit (`regMP0_SMN_C2PMSG_35`, bit 31) with a single,
non-retried poll. The budget is `AMDGV_TIMEOUT(TIMEOUT_PSP_REG)`, set to
`1000 * 1000` microseconds — **one second** — on real hardware
(`mi300_setup_common_timeout()`, `mi300_ip_discovery.c`). After an `amdgpu`
unload, all 8 adapters fail this wait with the register still reading
`0x00000000` at the one-second mark:

```text
gim warning libgv: [0:a8:0:0][amdgv_wait_for_timeout_print:657] Timeout: WAIT_FOR_REGISTER regMP0_SMN_C2PMSG_35 [0x16063]. Expect val_mask==0x80000000. Actual val=0x00000000. Elapsed=1004114
gim error libgv:   [0:a8:0:0][mi300_psp_load_psp_fw:1554] TIMEOUT waiting for GFX mailbox to open
gim error libgv:   [0:a8:0:0][PF][mi300_psp_hw_init:1815] Failed to init PSP.
```

`modprobe` returns `Input/output error`; all 8 PFs are left unbound. The
sibling function `mi300_psp_wait_for_bootloader_steady()`, ~90 lines away in
the same file, polls the identical register but wraps it in a
`PSP_WAIT_BOOTLOADER_RETRY` retry loop — `mi300_psp_load_psp_fw()`'s poll does
not retry. That asymmetry is the leading candidate for the bug. Filed as
[amd/MxGPU-Virtualization#29](https://github.com/amd/MxGPU-Virtualization/issues/29).

Log: `/home/jhull/dra-test-work/amdgpu-patch/gim-roundtrip-clean-20261003-153455.log`

### The local GIM patches amplify the failure but do not cause it

Once `mi300_psp_hw_init` fails on one adapter, the local failure-propagation
changes (`gim_init()` checking each PF init thread's result,
`amdgv_device_internal_init()` calling `goto fail` on HW-init failure) tear
down every adapter and fail the whole module load, rather than leaving the
other 7 PFs usable. The resulting per-adapter whole-GPU-reset unwind runs
serialized roughly 30 seconds apart (15:36:00, 15:36:30, 15:37:01, 15:37:31,
15:38:01 in the clean-run log) and each unwind also fails
`WAIT_FOR_PSP_BOOT_COMPLETE` with `fw status 0x800f0000`. This cascade is why
a reboot was required to recover — but it is a consequence of the PSP timeout
above, not an independent root cause. See
[amd/MxGPU-Virtualization#29](https://github.com/amd/MxGPU-Virtualization/issues/29)
for the secondary observations this surfaced (a box-wide `hive_lock`, and the
all-or-nothing failure propagation).

### Single-PF GIM loads are not a valid test on this hardware

`mi300_vbios_early_hw_init()` (`libgv/core/hw/AI/mi300/mi300_vbios.c:277-283`)
gates the GPU reload reset behind
`task_barrier_enter_timeout(&reload_reset_tb, adapt->xgmi.phy_nodes_num, …)`.
`phy_nodes_num` reflects the *physical* 8-node XGMI fabric topology, not how
many PFs are currently bound to GIM. A single-PF load can never satisfy that
barrier regardless of any other issue present; it always times out at
`TIMEOUT_CHAIN_RESET` (30s). The single-PF test run performed during this
investigation is inconclusive for this reason and should not be repeated or
cited as evidence about per-device behavior.

Log: `/home/jhull/dra-test-work/amdgpu-patch/gim-single-pf-20261003-154711.log`

### Reverse direction: VCN ring init failure on one PF (unresolved, AMD's to own)

Originally reported in
`docs/gim-amdgpu-transition-incident-2026-09-30.md` and tracked upstream as
[ROCm/amdgpu#234](https://github.com/ROCm/amdgpu/issues/234). One PF
(`0000:dc:00.0` in the original run) fails VCN ring init with `-110` during
the `amdgpu` probe that follows a GIM unload, and a subsequent probe can panic
in `amdgpu_in_reset()` on a freed `reset_domain`. This remains unresolved. A
candidate fix (cancel the VCN idle work in `amdgpu_vcn_sw_fini()` before
freeing VCN state) is described in the issue and preserved, unvalidated, at
[`johnahull/amdgpu` @ `fix/mi355x-vcn-teardown`](https://github.com/johnahull/amdgpu/tree/fix/mi355x-vcn-teardown).

Log: `/home/jhull/dra-test-work/amdgpu-patch/gim-to-amdgpu-patched-20261003-150234.log`

## Host

- SSH: `jhull@10.14.202.26`
- `jhull` has passwordless sudo on the test host.
- System: Dell PowerEdge XE9785L
- GPUs: 8x AMD Instinct MI355X PFs, PCI ID `1002:75a3`
- PF BDFs:

  ```text
  0000:0c:00.0  0000:3d:00.0  0000:a8:00.0  0000:dc:00.0
  0001:0d:00.0  0001:3d:00.0  0001:a5:00.0  0001:dc:00.0
  ```

- The host has two NUMA nodes and an eight-node XGMI fabric.
- Kernel under test: `7.2.8-200.fc44.x86_64`
- Previous kernel involved in the original incident: `7.2.7-200.fc44.x86_64`
- Baseline verified 2026-10-03: all 8 PFs `driver=amdgpu`, `sriov_numvfs=0`, no
  `driver_override` set, `gim` not loaded, `sudo -n true` OK.

## Firmware/driver identifiers observed

- VBIOS build: `00193069`
- VBIOS date: `07/08/26,01:49:53`
- VBIOS part: `MI355X_36G`
- ATOM BIOS: `113-M355-01-1K1-040C`
- SMU PMFW: `04561200`
- PMFW interface: `00860001`
- GIM driver interface: `00860000`

A Dell firmware page shown during this work identified MI355X baseboard
firmware package `GPNR`, version `01.26.01.03, A00`, released 2026-09-08. The
package/update completion was not independently verified from the host.

## Local source trees (preserved on forks, see above)

### amdgpu

Path: `/home/jhull/devel/amd/amdgpu`, branch `fix/mi355x-vcn-teardown`, pushed
to `johnahull/amdgpu`. Split into 4 commits:

1. `amdgpu_irq_put()` guards — directly matches the crash signature in the
   original 2026-09-30 incident; the most defensible of the four.
2. VCN idle-work cancellation on `sw_fini` — the candidate fix for
   [ROCm/amdgpu#234](https://github.com/ROCm/amdgpu/issues/234).
3. CPER/ring teardown-tracking hardening — speculative, not tied to a logged
   crash.
4. XCP failed-probe unplug tracking — speculative, not tied to a logged crash.

### GIM

Path: `/home/jhull/devel/amd/MxGPU-Virtualization`, branch
`mi355x-psp-handoff-investigation` (based on `staging` @ `7ebc33d`, release
9.2.0.K), pushed to `johnahull/MxGPU-Virtualization`. Split into 3 commits:

1. Propagate asynchronous PF init failures through `gim_init` instead of
   reporting a successful module load when a PF's init thread failed.
2. Restore PF PCI/SR-IOV/SDMA-routing state after a failed whole-GPU reset,
   and serialize the PSP bootloader-steady wait on `adapt->hive_lock` (later
   found to be a single process-wide lock, not per-hive — see
   [#29](https://github.com/amd/MxGPU-Virtualization/issues/29)).
3. Use checked `strscpy()` instead of unchecked `strncpy()`, needed for kernel
   7.2.8 compatibility.

None of these three commits touch `mi300_psp_load_psp_fw`,
`mi300_psp_wait_for_bootloader_steady`, or `TIMEOUT_PSP_REG` — the
forward-direction PSP timeout reproduces against stock GIM logic underneath
them.

## Build and install locations on the host

```text
/home/jhull/dra-test-work/build/MxGPU-Virtualization-patched
```

```bash
cd /home/jhull/dra-test-work/build/MxGPU-Virtualization-patched/gim
make KERNELDIR=/usr/src/kernels/7.2.8-200.fc44.x86_64 -j$(nproc)
```

Exact Fedora in-tree amdgpu source:

```text
/home/jhull/dra-test-work/fedora-kernel-src-7.2.8/rpmbuild/BUILD/kernel-7.2.8-build/kernel-7.2.8/linux-7.2.8-200.fc44.x86_64
```

Installed test modules:

```text
/lib/modules/7.2.8-200.fc44.x86_64/updates/amd-teardown/amdgpu.ko
/lib/modules/7.2.8-200.fc44.x86_64/updates/amd-teardown/gim.ko
/lib/modules/7.2.8-200.fc44.x86_64/updates/amd-teardown/amd-vfio-pci.ko
```

Backups:

```text
/home/jhull/dra-test-work/build/installed-backup-20261003-150020
/home/jhull/dra-test-work/build/installed-backup-20261003-150435-gim2
```

These should be preserved in case the investigation resumes.

## Driver override and module-operation details

PF driver overrides are controlled through:

```text
/sys/bus/pci/devices/<BDF>/driver_override
```

- Write `gim` to select GIM for a future bind.
- Clear the override with an empty line (`printf '\\n'`); `none` is not a
  clear operation and can intentionally prevent normal automatic binding.
- `/etc/modprobe.d/blacklist-amdgpu.conf` exists on the host with its
  blacklist directive commented out (`# disabled for amdgpu teardown test:
  blacklist amdgpu`) — convenient if an `amdgpu`-blacklisted boot is needed
  again.
- The current kernel cmdline already carries `modprobe.blacklist=gim
  rd.driver.pre=amdgpu`, which is why a clean reboot returns the host to an
  `amdgpu`-only state. `/etc/modprobe.d/gim.conf` is currently empty (0
  bytes); prior saved configurations exist as
  `gim.conf.pre-dpx-20261002-140307` and `gim.conf.pre-config-20261002-141228`
  (the latter has a two-VF option, unused in these tests).

## Test history and evidence

### Original GIM-to-amdgpu incident (2026-09-30)

See `docs/gim-amdgpu-transition-incident-2026-09-30.md`. The transition caused
a host crash/reboot while probing an `amdgpu` PF after GIM unload; four PFs
bound, the rest timed out, and the host rebooted. Now tracked as
[ROCm/amdgpu#234](https://github.com/ROCm/amdgpu/issues/234).

### 2026-10-03 round-trip attempts

Three attempts on the current kernel (`7.2.8-200.fc44.x86_64`), each followed
by a clean reboot:

1. **Clean all-8-PF attempt** — forward direction failed at the PSP-mailbox
   stage described above on all 8 adapters.
   (`gim-roundtrip-clean-20261003-153455.log`)
2. **Single-PF attempt** — invalid by the XGMI-barrier constraint described
   above; not meaningful evidence. (`gim-single-pf-20261003-154711.log`)
3. **Earlier successful GIM load, failed reverse transition** — on a
   differently-prepared host state, GIM loaded successfully (8 PFs, 8 VFs,
   CPX mode, memory partition mode 2, CC mode 3), unloaded cleanly, but the
   reverse `amdgpu` bind failed one PF (`0000:dc:00.0`) with the VCN `-110`
   failure tracked in #234.
   (`gim-to-amdgpu-patched-20261003-150234.log`)

One additional run
(`gim-roundtrip-no-vcn-route-20261003-150451.log`) was started without
rebooting after a failed reverse transition and is invalid evidence — GIM
failed with PSP mailbox/recovery timeouts from a dirty prior failure, not from
a clean starting state. Documented here only so it is not reused.

## Safety notes

- Preserve the installed-module backups listed above.
- Do not use `git reset --hard` or discard local changes on either source
  tree; both are preserved on the forks linked above.
- A failed live GIM probe may leave PSP/PF state unrecoverable without a host
  reboot; capture the kernel log before rebooting.
- A GIM VF is not a PF. Do not unbind a PF from `amdgpu` or treat a PF as a
  GIM VF unless the driver and PCI identity are explicitly verified.

## If this is picked back up

Only resume if AMD responds to
[ROCm/amdgpu#234](https://github.com/ROCm/amdgpu/issues/234) confirming the
transition is intended to be supported. In that case:

1. Start from the forward-direction PSP-timeout fix proposed in
   [amd/MxGPU-Virtualization#29](https://github.com/amd/MxGPU-Virtualization/issues/29)
   rather than re-deriving it.
2. Never repeat a partial-PF GIM load on this (or any XGMI-connected) board.
3. Re-validate the local GIM and amdgpu branches from scratch; both are
   unvalidated and were written while chasing a moving target.
4. Require at least 3 consecutive clean round trips from a cold boot before
   considering the transition reliable.
