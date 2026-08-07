#!/usr/bin/env python3
"""Tests for flash.py — port detection, JTAG reset, and flash orchestration.

Run with: python3 -m pytest scripts/test_flash.py -v
    or:   python3 scripts/test_flash.py
"""

import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, call, patch

# Import the module under test
sys.path.insert(0, str(Path(__file__).parent))
import flash


# ---------------------------------------------------------------------------
# Helpers to build fake sysfs trees
# ---------------------------------------------------------------------------

def make_sysfs_tty(tmpdir: str, tty_name: str, vid: str, pid: str,
                   serial: str = "AABBCCDD") -> None:
    """Create a minimal sysfs-like tree for a ttyACM device.

    Layout:
      <tmpdir>/sys/class/tty/<tty_name>/device -> ../../usb/<tty_name>-iface
      <tmpdir>/sys/usb/<tty_name>-iface/           (interface dir)
      <tmpdir>/sys/usb/                             (USB device dir with idVendor etc.)
    """
    tty_dir = Path(tmpdir) / "sys" / "class" / "tty" / tty_name
    usb_dev_dir = Path(tmpdir) / "sys" / "usb" / tty_name
    iface_dir = usb_dev_dir / "iface"

    # Create interface dir (where the tty device symlink points)
    iface_dir.mkdir(parents=True)

    # Create the USB device dir with VID/PID (parent of interface)
    (usb_dev_dir / "idVendor").write_text(vid)
    (usb_dev_dir / "idProduct").write_text(pid)
    (usb_dev_dir / "serial").write_text(serial)

    # Create tty dir with 'device' symlink pointing to the interface dir
    tty_dir.mkdir(parents=True)
    (tty_dir / "device").symlink_to(iface_dir)


# ---------------------------------------------------------------------------
# Port detection tests
# ---------------------------------------------------------------------------

class TestFindPortByVidPid(unittest.TestCase):
    """Test find_port_by_vid_pid() — sysfs-based USB port identification."""

    @patch("flash._by_id_path_for", return_value=None)
    def test_finds_matching_device(self, _mock_by_id):
        with tempfile.TemporaryDirectory() as tmpdir:
            make_sysfs_tty(tmpdir, "ttyACM0", "303a", "1002", "ESPPROG2")
            make_sysfs_tty(tmpdir, "ttyACM1", "303a", "1001", "FEATHER")
            sysfs = Path(tmpdir) / "sys" / "class" / "tty"

            port = flash.find_port_by_vid_pid("303a", "1001", sysfs_base=sysfs)
            self.assertEqual(port, "/dev/ttyACM1")

    @patch("flash._by_id_path_for", return_value=None)
    def test_finds_esp_prog2(self, _mock_by_id):
        with tempfile.TemporaryDirectory() as tmpdir:
            make_sysfs_tty(tmpdir, "ttyACM0", "303a", "1002", "ESPPROG2")
            make_sysfs_tty(tmpdir, "ttyACM1", "303a", "1001", "FEATHER")
            sysfs = Path(tmpdir) / "sys" / "class" / "tty"

            port = flash.find_port_by_vid_pid("303a", "1002", sysfs_base=sysfs)
            self.assertEqual(port, "/dev/ttyACM0")

    def test_returns_none_when_no_match(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            make_sysfs_tty(tmpdir, "ttyACM0", "303a", "1002", "ESPPROG2")
            sysfs = Path(tmpdir) / "sys" / "class" / "tty"

            port = flash.find_port_by_vid_pid("303a", "1001", sysfs_base=sysfs)
            self.assertIsNone(port)

    def test_returns_none_when_no_devices(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            sysfs = Path(tmpdir) / "sys" / "class" / "tty"
            sysfs.mkdir(parents=True)

            port = flash.find_port_by_vid_pid("303a", "1001", sysfs_base=sysfs)
            self.assertIsNone(port)

    def test_handles_missing_vid_file_gracefully(self):
        """Devices without idVendor (e.g. virtual ttys) should be skipped."""
        with tempfile.TemporaryDirectory() as tmpdir:
            # Create a tty without USB info
            tty_dir = Path(tmpdir) / "sys" / "class" / "tty" / "ttyACM0"
            tty_dir.mkdir(parents=True)
            # 'device' symlink points to a dir with no idVendor
            no_usb = Path(tmpdir) / "sys" / "nousb"
            no_usb.mkdir(parents=True)
            (tty_dir / "device").symlink_to(no_usb)

            sysfs = Path(tmpdir) / "sys" / "class" / "tty"
            port = flash.find_port_by_vid_pid("303a", "1001", sysfs_base=sysfs)
            self.assertIsNone(port)

    def test_picks_first_match_alphabetically(self):
        """When multiple devices match, return the first one."""
        with tempfile.TemporaryDirectory() as tmpdir:
            make_sysfs_tty(tmpdir, "ttyACM1", "303a", "1001", "FEATHER1")
            make_sysfs_tty(tmpdir, "ttyACM3", "303a", "1001", "FEATHER2")
            sysfs = Path(tmpdir) / "sys" / "class" / "tty"

            port = flash.find_port_by_vid_pid("303a", "1001", sysfs_base=sysfs)
            self.assertEqual(port, "/dev/ttyACM1")


class TestDetectPort(unittest.TestCase):
    """Test detect_port() — high-level port selection for each flash method."""

    @patch("flash.find_port_by_vid_pid")
    @patch("glob.glob")
    def test_usb_prefers_feather_vid_pid(self, mock_glob, mock_find):
        mock_find.return_value = "/dev/ttyACM1"
        result = flash.detect_port("usb")
        self.assertEqual(result, ("/dev/ttyACM1", "/dev/ttyACM1"))
        mock_find.assert_called_once_with("303a", "1001")

    @patch("flash.find_port_by_vid_pid")
    @patch("glob.glob")
    def test_usb_falls_back_to_first_non_espprog_ttyACM(self, mock_glob, mock_find):
        # First call: look for Feather (1001) -> None
        # Second call: look for ESP-Prog-2 (1002) -> ttyACM0
        mock_find.side_effect = [None, "/dev/ttyACM0"]
        mock_glob.return_value = ["/dev/ttyACM0", "/dev/ttyACM2"]
        result = flash.detect_port("usb")
        # Should skip ttyACM0 (ESP-Prog-2) and pick ttyACM2
        self.assertEqual(result, ("/dev/ttyACM2", "/dev/ttyACM2"))

    @patch("flash.find_port_by_vid_pid")
    @patch("glob.glob")
    def test_usb_no_device_raises(self, mock_glob, mock_find):
        mock_find.side_effect = [None, None]
        mock_glob.return_value = []
        with self.assertRaises(SystemExit):
            flash.detect_port("usb")

    @patch("flash.find_port_by_vid_pid")
    @patch("glob.glob")
    def test_usb_only_espprog2_raises(self, mock_glob, mock_find):
        """When only ESP-Prog-2 is present (target asleep), should error."""
        mock_find.side_effect = [None, "/dev/ttyACM0"]
        mock_glob.return_value = ["/dev/ttyACM0"]
        with self.assertRaises(SystemExit):
            flash.detect_port("usb")

    @patch("flash.find_port_by_vid_pid")
    def test_esp_prog2_finds_by_vid_pid(self, mock_find):
        mock_find.return_value = "/dev/ttyACM0"
        result = flash.detect_port("esp-prog-2")
        self.assertEqual(result, ("/dev/ttyACM0", "/dev/ttyACM0"))
        mock_find.assert_called_once_with("303a", "1002")

    @patch("flash.find_port_by_vid_pid")
    def test_esp_prog2_no_device_raises(self, mock_find):
        mock_find.return_value = None
        with self.assertRaises(SystemExit):
            flash.detect_port("esp-prog-2")

    @patch("glob.glob")
    def test_esp_prog_legacy_finds_ttyUSB(self, mock_glob):
        mock_glob.return_value = ["/dev/ttyUSB0", "/dev/ttyUSB1"]
        result = flash.detect_port("esp-prog")
        self.assertEqual(result, ("/dev/ttyUSB0", "/dev/ttyUSB1"))

    @patch("glob.glob")
    def test_esp_prog_legacy_needs_two_ports(self, mock_glob):
        mock_glob.return_value = ["/dev/ttyUSB0"]
        with self.assertRaises(SystemExit):
            flash.detect_port("esp-prog")


# ---------------------------------------------------------------------------
# JTAG reset tests
# ---------------------------------------------------------------------------

class TestJtagReset(unittest.TestCase):
    """Test jtag_reset_to_wake() — OpenOCD JTAG reset before flash."""

    @patch("shutil.which")
    def test_skips_when_no_openocd(self, mock_which):
        mock_which.return_value = None
        # Should not raise, just skip
        flash.jtag_reset_to_wake()
        mock_which.assert_called_once_with("openocd")

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_runs_openocd_reset(self, mock_which, mock_run):
        mock_which.return_value = "/usr/bin/openocd"
        mock_run.return_value = MagicMock(returncode=0)

        with patch.dict(os.environ, {}, clear=True):
            flash.jtag_reset_to_wake()

        mock_run.assert_called_once()
        cmd = mock_run.call_args[0][0]
        self.assertIn("openocd", cmd[0])
        # Should contain reset run and shutdown
        cmd_str = " ".join(cmd)
        self.assertIn("reset run", cmd_str)
        self.assertIn("shutdown", cmd_str)
        self.assertIn("esp32c6-bridge", cmd_str)

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_openocd_failure_does_not_raise(self, mock_which, mock_run):
        """JTAG reset failure is non-fatal — device might not be connected."""
        mock_which.return_value = "/usr/bin/openocd"
        mock_run.return_value = MagicMock(returncode=1, stderr="Error: something\n")
        with patch.dict(os.environ, {}, clear=True):
            flash.jtag_reset_to_wake()

    @patch("subprocess.run")
    @patch("shutil.which")
    def test_uses_openocd_scripts_env(self, mock_which, mock_run):
        """When OPENOCD_SCRIPTS is set to a valid dir, pass -s flag."""
        mock_which.return_value = "/nix/store/abc/bin/openocd"
        mock_run.return_value = MagicMock(returncode=0)

        with tempfile.TemporaryDirectory() as tmpdir:
            scripts_dir = Path(tmpdir) / "scripts"
            scripts_dir.mkdir()
            with patch.dict(os.environ, {"OPENOCD_SCRIPTS": str(scripts_dir)}, clear=True):
                flash.jtag_reset_to_wake()

        cmd = mock_run.call_args[0][0]
        self.assertIn("-s", cmd)
        idx = cmd.index("-s")
        self.assertEqual(cmd[idx + 1], str(scripts_dir))


# ---------------------------------------------------------------------------
# Flash function tests
# ---------------------------------------------------------------------------

class TestFlash(unittest.TestCase):
    """Test flash() — esptool invocation from flasher_args.json."""

    def _make_build_dir(self, tmpdir: str) -> Path:
        build_dir = Path(tmpdir) / "firmware"
        build_dir.mkdir()
        (build_dir / "bootloader").mkdir()
        (build_dir / "bootloader" / "bootloader.bin").write_bytes(b"\x00")
        (build_dir / "partition_table").mkdir()
        (build_dir / "partition_table" / "partition-table.bin").write_bytes(b"\x00")
        (build_dir / "app.bin").write_bytes(b"\x00")

        flasher_args = {
            "write_flash_args": ["--flash_mode", "dio", "--flash_freq", "80m", "--flash_size", "4MB"],
            "flash_files": {
                "0x0": "bootloader/bootloader.bin",
                "0x8000": "partition_table/partition-table.bin",
                "0x10000": "app.bin",
            },
            "extra_esptool_args": {
                "chip": "esp32c6",
                "before": "default_reset",
                "after": "hard_reset",
            },
        }
        (build_dir / "flasher_args.json").write_text(json.dumps(flasher_args))
        return build_dir

    @patch("subprocess.run")
    def test_flash_builds_correct_command(self, mock_subproc):
        mock_subproc.return_value = MagicMock(returncode=0, stdout="", stderr="")
        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = self._make_build_dir(tmpdir)
            flash.flash(build_dir, "/dev/ttyACM1", erase=False)

            mock_subproc.assert_called_once()
            cmd = mock_subproc.call_args[0][0]
            self.assertEqual(cmd[0], "esptool.py")
            self.assertIn("--chip", cmd)
            self.assertIn("esp32c6", cmd)
            self.assertIn("-p", cmd)
            self.assertIn("/dev/ttyACM1", cmd)
            self.assertIn("write_flash", cmd)
            # Check all three binaries are included
            cmd_str = " ".join(cmd)
            self.assertIn("bootloader.bin", cmd_str)
            self.assertIn("partition-table.bin", cmd_str)
            self.assertIn("app.bin", cmd_str)

    @patch("subprocess.run")
    @patch("flash.run")
    def test_flash_with_erase(self, mock_run, mock_subproc):
        mock_subproc.return_value = MagicMock(returncode=0, stdout="", stderr="")
        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = self._make_build_dir(tmpdir)
            flash.flash(build_dir, "/dev/ttyACM1", erase=True)

            # The erase must happen inside the single write_flash invocation. A
            # separate erase_flash run resets the chip, and on a native-USB part
            # that leaves no firmware to enumerate the CDC port, so the write
            # that follows cannot open it.
            mock_run.assert_not_called()
            mock_subproc.assert_called_once()
            write_cmd = mock_subproc.call_args[0][0]
            self.assertIn("write_flash", write_cmd)
            self.assertIn("--erase-all", write_cmd)
            self.assertNotIn("erase_flash", write_cmd)

    @patch("subprocess.run")
    @patch("flash.run")
    def test_flash_in_bootloader_skips_reset(self, mock_run, mock_subproc):
        """--in-bootloader must not let esptool reset the chip.

        A device in the ROM download loader has no firmware behind its CDC port,
        so the DTR/RTS toggle of a reset drops it off the USB bus entirely.
        """
        mock_subproc.return_value = MagicMock(returncode=0, stdout="", stderr="")
        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = self._make_build_dir(tmpdir)
            flash.flash(build_dir, "/dev/ttyACM1", erase=False, in_bootloader=True)

            write_cmd = mock_subproc.call_args[0][0]
            self.assertIn("no_reset", write_cmd)
            self.assertEqual("no_reset", write_cmd[write_cmd.index("--before") + 1])

    @patch("subprocess.run")
    @patch("flash.run")
    def test_flash_writes_extra_images_in_same_invocation(self, mock_run, mock_subproc):
        """NVS images must ride along with the firmware, not a follow-up call."""
        mock_subproc.return_value = MagicMock(returncode=0, stdout="", stderr="")
        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = self._make_build_dir(tmpdir)
            flash.flash(build_dir, "/dev/ttyACM1", erase=False,
                        extra_images=[("0x9000", Path("/tmp/nvs.bin"))])

            mock_subproc.assert_called_once()
            write_cmd = mock_subproc.call_args[0][0]
            self.assertIn("0x9000", write_cmd)
            self.assertIn("/tmp/nvs.bin", write_cmd)

    def test_flash_missing_flasher_args_raises(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = Path(tmpdir) / "empty"
            build_dir.mkdir()
            with self.assertRaises(SystemExit):
                flash.flash(build_dir, "/dev/ttyACM1", erase=False)


# ---------------------------------------------------------------------------
# Deep-sleep-aware flash workflow tests
# ---------------------------------------------------------------------------

class TestDeepSleepFlashWorkflow(unittest.TestCase):
    """Test that the main() flow calls jtag_reset when device uses deep sleep."""

    @patch("flash.monitor")
    @patch("flash.flash")
    @patch("flash.generate_nvs_partition", return_value=None)
    @patch("flash.detect_port", return_value=("/dev/ttyACM1", "/dev/ttyACM1"))
    @patch("flash.build")
    @patch("flash.jtag_reset_to_wake")
    @patch("flash.device_uses_deep_sleep", return_value=True)
    @patch("time.sleep")
    def test_jtag_reset_called_for_deep_sleep_device(
        self, mock_sleep, mock_deep_sleep, mock_jtag, mock_build,
        mock_detect, mock_nvs, mock_flash, mock_monitor
    ):
        mock_build.return_value = Path("/nix/store/fake-firmware")

        with patch("sys.argv", ["flash.py", "--no-monitor", "devices/laundry-detector"]):
            with patch("pathlib.Path.is_dir", return_value=True):
                try:
                    flash.main()
                except SystemExit:
                    pass  # argparse may exit

        mock_jtag.assert_called_once()

    @patch("flash.monitor")
    @patch("flash.flash")
    @patch("flash.generate_nvs_partition", return_value=None)
    @patch("flash.detect_port", return_value=("/dev/ttyACM0", "/dev/ttyACM0"))
    @patch("flash.build")
    @patch("flash.jtag_reset_to_wake")
    @patch("flash.device_uses_deep_sleep", return_value=False)
    def test_jtag_reset_not_called_for_non_sleep_device(
        self, mock_deep_sleep, mock_jtag, mock_build,
        mock_detect, mock_nvs, mock_flash, mock_monitor
    ):
        mock_build.return_value = Path("/nix/store/fake-firmware")

        with patch("sys.argv", ["flash.py", "--no-monitor", "devices/freezer-temp-sensor"]):
            with patch("pathlib.Path.is_dir", return_value=True):
                try:
                    flash.main()
                except SystemExit:
                    pass

        mock_jtag.assert_not_called()


# ---------------------------------------------------------------------------
# device_uses_deep_sleep tests
# ---------------------------------------------------------------------------

class TestDeviceUsesDeepSleep(unittest.TestCase):
    """Test device_uses_deep_sleep() — checks sdkconfig/source for deep sleep."""

    def test_laundry_detector_uses_deep_sleep(self):
        device_dir = Path(__file__).parent.parent / "devices" / "laundry-detector"
        if device_dir.exists():
            self.assertTrue(flash.device_uses_deep_sleep(device_dir))

    def test_freezer_does_not_use_deep_sleep(self):
        device_dir = Path(__file__).parent.parent / "devices" / "freezer-temp-sensor"
        if device_dir.exists():
            self.assertFalse(flash.device_uses_deep_sleep(device_dir))

    def test_nonexistent_dir_returns_false(self):
        self.assertFalse(flash.device_uses_deep_sleep(Path("/nonexistent")))


# ---------------------------------------------------------------------------
# Pairing info tests (#19)
# ---------------------------------------------------------------------------

class TestGetPairingInfo(unittest.TestCase):
    """Test get_pairing_info() — read pairing codes from matter-pairing.json."""

    def test_reads_valid_json(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            pairing_file = Path(tmpdir) / "matter-pairing.json"
            pairing_file.write_text(json.dumps({
                "qr_code": "MT:Y.K90AFN00KA0648G00",
                "manual_code": "34970112332",
            }))
            info = flash.get_pairing_info(Path(tmpdir))
            self.assertEqual(info["qr_code"], "MT:Y.K90AFN00KA0648G00")
            self.assertEqual(info["manual_code"], "34970112332")

    def test_returns_none_when_no_file(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            info = flash.get_pairing_info(Path(tmpdir))
            self.assertIsNone(info["qr_code"])
            self.assertIsNone(info["manual_code"])

    def test_returns_none_on_invalid_json(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            pairing_file = Path(tmpdir) / "matter-pairing.json"
            pairing_file.write_text("not valid json{{{")
            info = flash.get_pairing_info(Path(tmpdir))
            self.assertIsNone(info["qr_code"])
            self.assertIsNone(info["manual_code"])

    def test_partial_keys(self):
        """JSON with only qr_code returns None for manual_code."""
        with tempfile.TemporaryDirectory() as tmpdir:
            pairing_file = Path(tmpdir) / "matter-pairing.json"
            pairing_file.write_text(json.dumps({"qr_code": "MT:ABC"}))
            info = flash.get_pairing_info(Path(tmpdir))
            self.assertEqual(info["qr_code"], "MT:ABC")
            self.assertIsNone(info["manual_code"])

    def test_reads_real_device_files(self):
        """Verify the actual matter-pairing.json files in the repo."""
        for device in ["laundry-detector", "freezer-temp-sensor"]:
            device_dir = Path(__file__).parent.parent / "devices" / device
            if device_dir.exists():
                info = flash.get_pairing_info(device_dir)
                self.assertIsNotNone(info["qr_code"], f"{device} missing qr_code")
                self.assertIsNotNone(info["manual_code"], f"{device} missing manual_code")


class TestPrintPairingInfo(unittest.TestCase):
    """Test print_pairing_info() — display formatting."""

    def test_prints_both_codes(self):
        import io
        from contextlib import redirect_stdout
        buf = io.StringIO()
        with redirect_stdout(buf):
            flash.print_pairing_info({"qr_code": "MT:TEST", "manual_code": "12345"})
        output = buf.getvalue()
        self.assertIn("MT:TEST", output)
        self.assertIn("12345", output)
        self.assertIn("Matter Pairing Info", output)

    def test_prints_nothing_when_no_codes(self):
        import io
        from contextlib import redirect_stdout
        buf = io.StringIO()
        with redirect_stdout(buf):
            flash.print_pairing_info({"qr_code": None, "manual_code": None})
        self.assertEqual(buf.getvalue(), "")


# ---------------------------------------------------------------------------
# Monitor with reconnect tests (#20)
# ---------------------------------------------------------------------------

class TestMonitorWithReconnect(unittest.TestCase):
    """Test monitor_with_reconnect() — handles USB disconnect/reconnect."""

    @patch("sys.stdin")
    @patch("flash.find_port_by_vid_pid")
    @patch("time.sleep")
    @patch("subprocess.run")
    def test_reconnects_after_disconnect(self, mock_run, mock_sleep, mock_find, mock_stdin):
        """Monitor exits on disconnect, waits for port, then restarts."""
        mock_stdin.isatty.return_value = True  # force TTY path
        # First run: exits with error (USB disconnect)
        # Second run: user hits Ctrl-C (KeyboardInterrupt simulated via side_effect)
        mock_run.side_effect = [
            MagicMock(returncode=1),  # first monitor exits (disconnect)
            KeyboardInterrupt(),       # second monitor, user quits
        ]
        # Port reappears on second check
        mock_find.side_effect = [None, None, "/dev/ttyACM1"]

        try:
            flash.monitor_with_reconnect(
                Path("/fake/build"), "/dev/ttyACM1",
                elf_path=Path("/fake/app.elf"), target="esp32c6",
            )
        except KeyboardInterrupt:
            pass

        # Should have called subprocess.run twice (two monitor attempts)
        self.assertEqual(mock_run.call_count, 2)

    @patch("sys.stdin")
    @patch("subprocess.run")
    def test_clean_exit_on_ctrl_c(self, mock_run, mock_stdin):
        """Ctrl-C during monitor should exit cleanly."""
        mock_stdin.isatty.return_value = True
        mock_run.side_effect = KeyboardInterrupt()

        # Should not raise
        flash.monitor_with_reconnect(
            Path("/fake/build"), "/dev/ttyACM1",
            elf_path=Path("/fake/app.elf"), target="esp32c6",
        )

    @patch("sys.stdin")
    @patch("subprocess.run")
    def test_clean_exit_on_zero_return(self, mock_run, mock_stdin):
        """Monitor exiting cleanly (rc=0) should not reconnect."""
        mock_stdin.isatty.return_value = True
        mock_run.return_value = MagicMock(returncode=0)

        flash.monitor_with_reconnect(
            Path("/fake/build"), "/dev/ttyACM1",
            elf_path=Path("/fake/app.elf"), target="esp32c6",
        )

        mock_run.assert_called_once()


# ---------------------------------------------------------------------------
# Thread NVS partition generation tests
# ---------------------------------------------------------------------------

class TestThreadNvsFlashGuard(unittest.TestCase):
    """Test that --thread only flashes NVS when --erase is also set."""

    @patch("flash.monitor")
    @patch("flash.flash")
    @patch("flash.generate_thread_nvs_partition")
    @patch("flash.generate_nvs_partition", return_value=None)
    @patch("flash.detect_port", return_value=("/dev/ttyACM1", "/dev/ttyACM1"))
    @patch("flash.build")
    @patch("flash.jtag_reset_to_wake")
    @patch("flash.device_uses_deep_sleep", return_value=True)
    @patch("time.sleep")
    def test_thread_nvs_skipped_without_erase(
        self, mock_sleep, mock_deep_sleep, mock_jtag, mock_build,
        mock_detect, mock_nvs, mock_thread_nvs, mock_flash, mock_monitor
    ):
        """--thread without --erase should NOT call generate_thread_nvs_partition."""
        mock_build.return_value = Path("/nix/store/fake-firmware")

        with patch("sys.argv", ["flash.py", "--no-monitor", "--thread", "devices/laundry-detector"]):
            with patch("pathlib.Path.is_dir", return_value=True):
                try:
                    flash.main()
                except SystemExit:
                    pass

        mock_thread_nvs.assert_not_called()

    @patch("flash.monitor")
    @patch("flash.flash")
    @patch("flash.generate_thread_nvs_partition")
    @patch("flash.generate_nvs_partition", return_value=None)
    @patch("flash.detect_port", return_value=("/dev/ttyACM1", "/dev/ttyACM1"))
    @patch("flash.build")
    @patch("flash.device_uses_deep_sleep", return_value=False)
    @patch("time.sleep")
    def test_garage_nvs_generated_without_erase(
        self, mock_sleep, mock_deep_sleep, mock_build,
        mock_detect, mock_nvs, mock_thread_nvs, mock_flash, mock_monitor
    ):
        """garage-opener secrets must be provisioned on a plain flash.

        They used to be gated on --erase alongside the Matter devices, but
        garage-opener has no fabric data to protect, so a normal flash left the
        device unprovisioned and it booted straight to "NVS open failed".
        """
        mock_build.return_value = Path("/nix/store/fake-firmware")

        with patch("sys.argv", ["flash.py", "--no-monitor", "devices/garage-opener"]):
            with patch("pathlib.Path.is_dir", return_value=True):
                try:
                    flash.main()
                except SystemExit:
                    pass

        mock_nvs.assert_called_once()

    @patch("flash.monitor")
    @patch("flash.flash")
    @patch("flash.generate_thread_nvs_partition")
    @patch("flash.generate_nvs_partition", return_value=None)
    @patch("flash.detect_port", return_value=("/dev/ttyACM1", "/dev/ttyACM1"))
    @patch("flash.build")
    @patch("flash.jtag_reset_to_wake")
    @patch("flash.device_uses_deep_sleep", return_value=True)
    @patch("time.sleep")
    def test_thread_nvs_flashed_with_erase(
        self, mock_sleep, mock_deep_sleep, mock_jtag, mock_build,
        mock_detect, mock_nvs, mock_thread_nvs, mock_flash, mock_monitor
    ):
        """--thread with --erase SHOULD call generate_thread_nvs_partition."""
        mock_build.return_value = Path("/nix/store/fake-firmware")
        fake_bin = Path("/tmp/fake-thread.bin")
        mock_thread_nvs.return_value = MagicMock()  # non-None = NVS bin path

        with patch("sys.argv", ["flash.py", "--no-monitor", "--thread", "--erase", "devices/laundry-detector"]):
            with patch("pathlib.Path.is_dir", return_value=True):
                with patch("builtins.open", unittest.mock.mock_open(read_data='{"extra_esptool_args": {"chip": "esp32c6"}}')):
                    try:
                        flash.main()
                    except SystemExit:
                        pass

        mock_thread_nvs.assert_called_once()


class TestGenerateThreadNvs(unittest.TestCase):
    """Test generate_thread_nvs_partition() — Thread credential injection via NVS."""

    SAMPLE_TLV_HEX = (
        "0e080000000000010000000300000f4a0300001935060004001fffe0"
        "0208ee05a9d2c8031b890708fdb923a9d342bb3d05109adbf9f1e5"
        "5a8db015f808106a06dab4030e68612d7468726561642d35353037"
        "010255070410e017a5c81cd05bb05bd689c8848c33a50c0402a0f7f8"
    )

    @patch("flash.decrypt_sops")
    @patch("flash.run")
    def test_thread_nvs_created_when_flag_set(self, mock_run, mock_decrypt):
        """When SOPS decrypts successfully, generate NVS CSV with correct content."""
        mock_decrypt.return_value = {"THREAD_DATASET_TLV": self.SAMPLE_TLV_HEX}

        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = Path(tmpdir) / "build"
            build_dir.mkdir()
            sops_file = Path(tmpdir) / "thread.sops.yaml"
            sops_file.write_text("encrypted")

            result = flash.generate_thread_nvs_partition(build_dir, sops_file)
            self.assertIsNotNone(result)

            # Verify nvs_partition_gen.py was called
            mock_run.assert_called_once()
            cmd = mock_run.call_args[0][0]
            cmd_str = " ".join(cmd)
            self.assertIn("nvs_partition_gen", cmd_str)
            self.assertIn("generate", cmd_str)

    @patch("flash.decrypt_sops")
    def test_thread_sops_file_missing_error(self, mock_decrypt):
        """Should raise SystemExit when SOPS file doesn't exist."""
        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = Path(tmpdir) / "build"
            build_dir.mkdir()
            sops_file = Path(tmpdir) / "nonexistent.sops.yaml"

            with self.assertRaises(SystemExit):
                flash.generate_thread_nvs_partition(build_dir, sops_file)

    @patch("flash.decrypt_sops")
    def test_thread_dataset_key_missing_error(self, mock_decrypt):
        """Should raise SystemExit when THREAD_DATASET_TLV key is absent."""
        mock_decrypt.return_value = {"SOME_OTHER_KEY": "value"}

        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = Path(tmpdir) / "build"
            build_dir.mkdir()
            sops_file = Path(tmpdir) / "thread.sops.yaml"
            sops_file.write_text("encrypted")

            with self.assertRaises(SystemExit):
                flash.generate_thread_nvs_partition(build_dir, sops_file)

    @patch("flash.decrypt_sops")
    @patch("flash.run")
    def test_thread_nvs_csv_content(self, mock_run, mock_decrypt):
        """Verify the generated CSV has correct namespace, key, and hex2bin encoding."""
        mock_decrypt.return_value = {"THREAD_DATASET_TLV": self.SAMPLE_TLV_HEX}

        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = Path(tmpdir) / "build"
            build_dir.mkdir()
            sops_file = Path(tmpdir) / "thread.sops.yaml"
            sops_file.write_text("encrypted")

            # Intercept the CSV before it gets deleted
            csv_contents = []
            original_run = flash.run.__wrapped__ if hasattr(flash.run, '__wrapped__') else None

            def capture_csv(cmd, **kwargs):
                # Find the CSV path in the command args
                for arg in cmd:
                    if arg.endswith(".csv"):
                        csv_contents.append(Path(arg).read_text())
                        break

            mock_run.side_effect = capture_csv

            flash.generate_thread_nvs_partition(build_dir, sops_file)

            self.assertTrue(len(csv_contents) > 0, "CSV was not captured")
            csv_text = csv_contents[0]
            # Check header
            self.assertIn("key,type,encoding,value", csv_text)
            # Check namespace
            self.assertIn("thread,namespace", csv_text)
            # Check dataset key with hex2bin encoding
            self.assertIn("dataset_tlv", csv_text)
            self.assertIn("hex2bin", csv_text)
            self.assertIn(self.SAMPLE_TLV_HEX, csv_text)

    @patch("flash.decrypt_sops")
    @patch("flash.run")
    def test_thread_nvs_csv_cleaned_up(self, mock_run, mock_decrypt):
        """CSV file containing plaintext secrets should be deleted after generation."""
        mock_decrypt.return_value = {"THREAD_DATASET_TLV": self.SAMPLE_TLV_HEX}

        csv_path_holder = []

        def capture_and_create_bin(cmd, **kwargs):
            for arg in cmd:
                if arg.endswith(".csv"):
                    csv_path_holder.append(Path(arg))
                if arg.endswith(".bin"):
                    # Create the bin file so the function can proceed
                    Path(arg).write_bytes(b"\x00" * 100)

        mock_run.side_effect = capture_and_create_bin

        with tempfile.TemporaryDirectory() as tmpdir:
            build_dir = Path(tmpdir) / "build"
            build_dir.mkdir()
            sops_file = Path(tmpdir) / "thread.sops.yaml"
            sops_file.write_text("encrypted")

            flash.generate_thread_nvs_partition(build_dir, sops_file)

        self.assertTrue(len(csv_path_holder) > 0, "CSV path was not captured")
        self.assertFalse(csv_path_holder[0].exists(), "CSV should be deleted after NVS gen")


if __name__ == "__main__":
    unittest.main()
