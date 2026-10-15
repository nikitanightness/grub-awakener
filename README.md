# GRUB Awakener

A lightweight service that lets you wake up a PC over LAN and pick which OS to boot into (in my setup: **Proxmox** or
**Windows**) directly from GRUB.

---

## About the Project

I travel often and frequently need to start local servers remotely while away from home. Standard Wake-on-LAN powers on
the machine, but it always boots into the default OS unless you are sitting at the physical keyboard to select another
one in GRUB.

This service solves that problem: it runs as a lightweight daemon on the router, sends the WoL magic packet when
requested, and serves the desired boot entry to GRUB via HTTP during boot.

### Why Go?

Originally, I planned to write this backend in Python. However, since the service runs directly on a home router
(**Keenetic Ultra KN-1811**), I didn't want the extra overhead of a Python runtime, virtual environments, and
dependencies.

I chose **Golang** because it compiles down to a single, small standalone binary without external dependencies. This was
actually my first time writing Go! Overall, I really liked it, even though it has a few language-specific quirks.

---

## How It Works

1. You send an HTTP POST request to `grub-awakener` on your router specifying the target OS.
2. The service saves the requested `boot_entry_id` (with a TTL) and broadcasts a WoL magic packet.
3. The PC turns on and GRUB initializes its network/HTTP modules.
4. GRUB queries `http://<ROUTER_IP>:<PORT>/v1/awakening-info?reset=true&format=grub-vars` to read and reset the state.
5. If awakened via WoL, GRUB sets the default entry to the requested OS and boots it immediately.

---

## Build

The included `scripts/build.sh` compiles the project for **Keenetic Ultra KN-1811** (`linux/arm64`):

```bash
chmod +x scripts/build.sh
./scripts/build.sh
```

---

## Usage

### 1. Run on router

```bash
./grub-awakener -mac-addr AA:BB:CC:DD:EE:FF -wol-ip-addr 192.168.1.255 -port 9009
```

### 2. Find Boot Entry IDs in GRUB

To see the available menu entries and their IDs on your Linux machine, run directly from GitHub:

```bash
curl -fsSL https://raw.githubusercontent.com/nikitanightness/grub-awakener/main/scripts/list-boot-entry-ids.sh | sh
```

Or run the local script:

```bash
chmod +x scripts/list-boot-entry-ids.sh
./scripts/list-boot-entry-ids.sh
```

> [!NOTE]
> **Custom Human-Readable IDs:**
> You can manually edit `/boot/grub/grub.cfg` to assign simple, readable IDs to each `menuentry` (e.g., `--id proxmox` or `--id windows`).
> 
> *Warning:* Since `/boot/grub/grub.cfg` is auto-generated, any manual modifications will be overwritten whenever `update-grub` or `grub-mkconfig` is executed.

### 3. GRUB Configuration

#### Quick Install (directly from GitHub)

Run the interactive setup script directly from GitHub:

```bash
curl -fsSL https://raw.githubusercontent.com/nikitanightness/grub-awakener/main/scripts/install-grub-config.sh | sudo sh
```

Or run the local script:

```bash
sudo ./scripts/install-grub-config.sh
```

The script will prompt you for `awakener_host`, `awakener_port`, write `/etc/grub.d/09_awakener`, and automatically offer to update GRUB.

#### Manual Installation

Alternatively, place [`grub.d/09_awakener`](grub.d/09_awakener) into `/etc/grub.d/09_awakener`, configure your router IP and port inside the file, make it executable, and update GRUB:

```bash
sudo chmod +x /etc/grub.d/09_awakener
sudo update-grub
```

### 4. Windows Dual-Boot Note (Important for WoL)

> [!IMPORTANT]
> If you dual-boot with Windows, you **must** configure your network adapter's power management settings in Windows. Otherwise, after shutting down from Windows, the network adapter will power off completely and Wake-on-LAN will stop working on subsequent boot attempts.
>
> 1. Open **Device Manager** (`devmgmt.msc`) in Windows.
> 2. Expand **Network adapters**, right-click your Ethernet adapter, and open **Properties**.
> 3. Go to the **Power Management** tab and ensure **all three** checkboxes are checked:
>    - [x] **Allow the computer to turn off this device to save power**
>    - [x] **Allow this device to wake the computer**
>    - [x] **Only allow a magic packet to wake the computer**
> 4. Go to the **Advanced** tab and ensure **Wake on Magic Packet** is set to **Enabled**.
> 5. *(Optional but recommended)* Disable **Fast Startup** in Windows (*Control Panel -> Power Options -> Choose what the power buttons do*), as Fast Startup can prevent the network card from staying in a WoL-ready standby state.

### 5. Wake up via `curl`

* **Boot into Proxmox:**
  ```bash
  curl -X POST http://192.168.1.1:9009/v1/wake-up \
       -H "Content-Type: application/json" \
       -d '{"boot_entry_id": "proxmox"}'
  ```

* **Boot into Windows:**
  ```bash
  curl -X POST http://192.168.1.1:9009/v1/wake-up \
       -H "Content-Type: application/json" \
       -d '{"boot_entry_id": "windows"}'
  ```

* **Default boot:**
  ```bash
  curl -X POST http://192.168.1.1:9009/v1/wake-up
  ```

*P.S. Sorry, I didn’t even read this README file carefully; it’s just there for the sake of it, it was completely generated by AI.*
<!--suppress HtmlDeprecatedAttribute -->
<p align="center">
  <sub>Made with ❤️ by <strong>renzee</strong></sub>
</p>
