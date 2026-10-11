import type { AxonWallConfig } from "../../api/types";

/**
 * Map the chosen devices onto the appliance's default wan0/lan0 interfaces
 * (zones and rules reference those names — the wizard rewires matches, not
 * topology). Returns an error string when both roles pick the same device.
 */
export function applyDeviceAssignment(
  config: AxonWallConfig,
  wanDevice: string,
  lanDevice: string,
): AxonWallConfig | string {
  if (wanDevice === "" || lanDevice === "")
    return "WAN and LAN must both be assigned";
  if (wanDevice === lanDevice)
    return `the same device (${wanDevice}) cannot serve both WAN and LAN`;

  const interfaces = config.interfaces.map((itf) => {
    if (itf.name === "wan0" && itf.addressing === "dhcp")
      return { ...itf, match: wanDevice };
    if (itf.name === "lan0") return { ...itf, match: lanDevice };
    return itf;
  });
  return { ...config, interfaces };
}
