# GIM to AMDGPU Transition Incident

> **Update 2026-10-03:** this incident is tracked upstream as
> [ROCm/amdgpu#234](https://github.com/ROCm/amdgpu/issues/234). The broader
> live-transition investigation is now parked — see
> [`AMDGPU-GIM-HANDOFF.md`](../AMDGPU-GIM-HANDOFF.md) for the current status
> and root-cause findings. This document is kept as the original incident
> record and is not updated further.

Date: 2026-09-30  
Host: `jhull@10.14.202.26` / Dell PowerEdge XE9785L  
Kernel: `7.2.7-200.fc44.x86_64`  
GPU: 8x AMD Instinct MI355X PFs (`1002:75a3`)

## Summary

An attempted live transition from GIM to `amdgpu` caused the host to crash and
reboot. Kubernetes recovered after the reboot. The failure occurred during
`amdgpu` PF initialization after GIM had been unloaded; it was not caused by a
Kubernetes workload or the DRA harness.

Do not repeat the live GIM unload and PF rebind procedure. Non-GIM testing
should use a controlled boot into an `amdgpu`-only configuration.

## Sequence

Before the transition:

- All 8 MI355X PFs were bound to `gim`.
- Each PF had one SR-IOV VF.
- The VFs had been unbound from `vfio-pci`.
- No Kubernetes VMs, VMIs, or processes holding VFIO/DRM device files were
  active.

During the transition:

1. VFs were unbound from `vfio-pci`.
2. Setting `sriov_numvfs=0` failed with `No such file or directory` on the
   first PF. This is a GIM-specific limitation or behavior; the operation did
   not complete as intended.
3. `modprobe -r gim` completed.
4. `amdgpu` was loaded and PF binding was started using PCI driver probing.
5. Four PFs initially bound to `amdgpu`; probing the remaining PFs timed out,
   SSH became unavailable, and the host rebooted.

## Kernel evidence

The previous boot journal records the following at approximately
`15:59:22`:

```text
amdgpu 0000:dc:00.0: [drm:amdgpu_ring_test_helper [amdgpu]] *ERROR* ring vcn_unified_0 test failed (-110)
amdgpu 0000:dc:00.0: hw_init of IP block <vcn_v5_0_1> failed -110
amdgpu 0000:dc:00.0: amdgpu_device_ip_init failed
amdgpu 0000:dc:00.0: Fatal error during GPU init
```

That was followed by repeated warnings from the `amdgpu` probe cleanup path:

```text
WARNING: drivers/gpu/drm/amd/amdgpu/amdgpu_irq.c:670 at amdgpu_irq_put+0x5e/0xb0 [amdgpu]
RIP: 0010:amdgpu_irq_put+0x5e/0xb0 [amdgpu]
amdgpu_fence_driver_hw_fini+0x104/0x160 [amdgpu]
amdgpu_device_fini_hw+0xbe/0x1a5 [amdgpu]
amdgpu_driver_load_kms.cold+0x22/0x44 [amdgpu]
amdgpu_pci_probe+0x1ab/0x5e0 [amdgpu]
```

The kernel reported the unloaded out-of-tree module:

```text
Unloaded tainted modules: gim(OE):1 [last unloaded: gim(OE)]
```

`last -x` recorded the prior session as a crash, consistent with an abrupt
kernel/host failure rather than a planned reboot. The logs do not establish
whether the root cause is GIM teardown, `amdgpu`, firmware, or their
interaction. They do establish that the failure was triggered during the
GIM-to-`amdgpu` handoff and `amdgpu` probe.

## State after reboot

- GIM was loaded again automatically.
- All 8 PFs were bound to `gim`.
- Each PF again reported `sriov_numvfs=1`.
- `vfio_pci` and related VFIO modules were loaded.
- Kubernetes 1.37.1 recovered and the node returned `Ready`.

The module configuration included:

```text
options gim vf_num=1
softdep gim
alias symbol:gim_get_mig_info gim
```

This explains why a reboot returned the host to the GIM/SR-IOV state instead
of an `amdgpu`-only state. The exact boot-time autoload path still needs to be
identified before changing it.

## Follow-up checklist

- Preserve the previous-boot journal and this incident record.
- Identify whether GIM is loaded from the initramfs, a systemd unit, or another
  boot-time module-loading path.
- For non-GIM testing, disable GIM autoload in a controlled configuration and
  rebuild the initramfs if required before rebooting.
- Verify all PFs bind to `amdgpu` after boot, without live unloading GIM.
- Capture `dmesg`, PCI bindings, firmware versions, and GIM/`amdgpu` versions
  before attempting further tests.
- Investigate the VCN ring timeout and `amdgpu_irq_put()` warning as a
  reproducible GIM/`amdgpu` handoff issue.

