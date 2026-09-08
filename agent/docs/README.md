# USBridge Agent Documentation

The Agent turns any Windows, macOS, or Linux machine into something the [USBridge Client](../../client/docs/README.md) can connect to and control — **no hardware required**. It shares the same pairing protocol and much of the same codebase as the physical [USBridge-KVM 2.0](https://github.com/USBridge-Technologies/USBridge-KVM-2.0) appliance, but it's software running *on* the machine you're accessing, not a hardware bridge sitting *in front of* it. See the [top-level README](../README.md) for downloads and a feature overview.

---

## Agent vs. Hardware KVM

This is the one thing to understand before anything else: the client talks to both the same way, but they are not equivalent.

| Capability | Software Agent | Hardware USBridge-KVM |
| :--- | :---: | :---: |
| Screen capture | ✅ (software, via Sunshine/RustShine) | ✅ (direct HDMI capture, works pre-OS/BIOS-level) |
| Keyboard/mouse input | ✅ (software injection: `SendInput`/`CGEvent`/direct) | ✅ (physical USB HID gadget — works before any OS loads) |
| System audio capture | ✅ | ✅ (HDMI audio extraction) |
| Power / hard reset control | ❌ | ✅ — [Power Management Module](https://github.com/USBridge-Technologies/USBridge-KVM-2.0/blob/main/docs/content/6-hardware-connectivity/power-management-module-control.md) |
| Virtual media (mount `.iso`/`.img`) | ❌ | ✅ |
| Internet sharing to the target | ❌ | ✅ — USB-LAN/RNDIS bridge |
| Versioned/immutable backup storage | ❌ | ✅ — Btrfs snapshots |
| MCP server (AI agent access) | ❌ | ✅ |
| Remote Starlark script execution | ❌ | ✅ |

The rule of thumb: the Agent gives you everything a **software remote-desktop tool** can give you — because that's exactly what it is, an OS-level agent. Anything that needs to act **before or independently of** that OS — power-cycling a frozen machine, mounting install media for a bare-metal OS install, reading BIOS/UEFI screens, surviving that OS being fully compromised — needs the physical hardware KVM instead. If you need that, the Agent and the hardware unit aren't competing options; they're complementary, and the same client manages both from one dashboard.

---

## Quick Start

1. Run the Agent on the machine you want to access. It displays a Master QR pairing token plus its LAN and Tailscale addresses.
2. Open the Client anywhere else, scan (or enter) the token, and connect.

That's the whole setup. See the [top-level README](../README.md#-quick-start) for platform-specific download links.

---

## Reference

* **[Auto-Update](../../docs/AUTO_UPDATE.md)** — how the Agent verifies and applies updates, including headless/silent-update behavior and the separate RustShine update channel.
* **[API Endpoints](../../client/docs/api_endpoints.md)** — the Master QR Sync pairing protocol and signed-request scheme; identical whether the client is talking to an Agent or a hardware KVM.
* **[Security & Authentication Model](https://github.com/USBridge-Technologies/USBridge-KVM-2.0/blob/main/docs/content/10-developer-api/security-model.md)** — the same layered pairing/signing/streaming security model used across the whole USBridge ecosystem, written up in full on the hardware KVM's docs (the Agent doesn't have a separate write-up because there's nothing different to say — it's the same scheme).

### Platform Notes (from the top-level README)

* **Wayland (Linux):** full screen capture and input injection with no permission-prompt spam — KMS capture needs one `pkexec` grant, which persists across reboots.
* **Launch at Login:** reflects your OS's actual autostart state live (no separate on/off flag of its own); always launches with `--headless` so the engine comes up silently and a later normal launch just attaches a GUI to it.
* **GPU Clock Lock (Windows + NVIDIA):** holds an NVML max-clock lock for the streaming session so the encoder doesn't stall waiting on a GPU that idled down between frames.
* **RustShine / WebRTC (Patreon):** the standard Sunshine backend doesn't support WebRTC, so the [Web Client](https://web.usbridge.io) needs the Patreon-gated RustShine streaming engine to connect to an Agent. Toggle it from the Permissions column once unlocked.
* **Lock screen / UAC (Windows):** the Windows Agent runs the streaming backend as SYSTEM inside the active session rather than the logged-in user's own token, so capture and input keep working straight through Win+L, sign-out to the logon screen, and UAC prompts — not just the ordinary unlocked desktop. It can also raise a synthetic Ctrl+Alt+Del on demand (`App.SendSAS()` / `POST /token/send-sas` on the Agent's local admin API) for machines where "require CTRL+ALT+DEL" is enabled, since Windows deliberately blocks ordinary keyboard injection from reaching that gesture on its own.

See [`../README.md`](../README.md) for the full detail on each of these — this page indexes it, it doesn't duplicate it.
