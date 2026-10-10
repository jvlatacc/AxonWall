import { describe, expect, it } from "vitest";
import type { AxonWallConfig } from "../../api/types";
import { applyDeviceAssignment } from "./deviceAssignment";

function configWith(match: string): AxonWallConfig {
  return {
    version: 1,
    zones: { wan: { interfaces: ["wan0"] }, lan: { interfaces: ["lan0"] } },
    interfaces: [
      { name: "wan0", match, addressing: "dhcp" },
      { name: "lan0", match: "other0", address: ["192.168.1.1/24"] },
    ],
    services: {},
    firewall: { default: { input: "drop", forward: "drop", output: "accept" } },
    aliases: [],
    nat: [],
    rules: [],
  } as unknown as AxonWallConfig;
}

describe("applyDeviceAssignment", () => {
  it("rewires wan0/lan0 matches while preserving addressing and topology", () => {
    const config = configWith("enp1s0");
    const next = applyDeviceAssignment(config, "eth-wan", "eth-lan");
    expect(typeof next).not.toBe("string");
    if (typeof next === "string") return;
    const wan0 = next.interfaces.find((itf) => itf.name === "wan0");
    const lan0 = next.interfaces.find((itf) => itf.name === "lan0");
    expect(wan0?.match).toBe("eth-wan");
    expect(wan0?.addressing).toBe("dhcp");
    expect(lan0?.match).toBe("eth-lan");
    expect(lan0?.address).toEqual(["192.168.1.1/24"]);
    expect(next.zones).toEqual(config.zones);
  });

  it("rejects assigning the same device to both roles", () => {
    expect(
      applyDeviceAssignment(configWith("enp1s0"), "eth0", "eth0"),
    ).toContain("cannot serve both");
  });

  it("rejects an unassigned role", () => {
    expect(
      applyDeviceAssignment(configWith("enp1s0"), "", "eth-lan"),
    ).toContain("must both be assigned");
  });
});
