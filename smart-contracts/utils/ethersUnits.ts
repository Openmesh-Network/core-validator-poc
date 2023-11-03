// Various ether units per Solidity spec

export const wei = BigInt(1);
export const gwei = BigInt(10) ** BigInt(9);
export const ether = BigInt(10) ** BigInt(18);

export function NumberWithBase(number: number, base: bigint): bigint {
  return base * BigInt(number);
}

export function Wei(number: number): bigint {
  return NumberWithBase(number, wei);
}

export function Gwei(number: number): bigint {
  return NumberWithBase(number, gwei);
}

export function Ether(number: number): bigint {
  return NumberWithBase(number, ether);
}
