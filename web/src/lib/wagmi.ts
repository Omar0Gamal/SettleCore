import { createConfig, http } from "wagmi";
import { sepolia } from "wagmi/chains";
import { injected } from "wagmi/connectors";
import { defineChain, type Address } from "viem";

export const anvilChain = defineChain({
  id: 31337,
  name: "Anvil Local",
  nativeCurrency: {
    name: "Ether",
    symbol: "ETH",
    decimals: 18,
  },
  rpcUrls: {
    default: {
      http: [process.env.NEXT_PUBLIC_ANVIL_RPC_URL ?? "http://localhost:8545"],
    },
  },
  testnet: true,
});

export const wagmiConfig = createConfig({
  chains: [anvilChain, sepolia],
  connectors: [injected()],
  transports: {
    [anvilChain.id]: http(process.env.NEXT_PUBLIC_ANVIL_RPC_URL ?? "http://localhost:8545"),
    [sepolia.id]: http(process.env.NEXT_PUBLIC_SEPOLIA_RPC_URL ?? "https://rpc.sepolia.org"),
  },
  ssr: true,
});

export const ESCROW_VAULT_ADDRESS: Address = (process.env.NEXT_PUBLIC_ESCROW_VAULT_ADDRESS ??
  "0x5FbDB2315678afecb367f032d93F642f64180aa3") as Address;

export type CustodyAsset = "ETH" | "USDC" | "WBTC" | "USDT";

export interface AssetSpec {
  symbol: CustodyAsset;
  name: string;
  address: Address;
  decimals: number;
  hotPerTxCap: string;
  hotDailyCap: string;
  isNative: boolean;
  isNonStandardErc20: boolean;
}

export const ASSET_SPECS: Record<CustodyAsset, AssetSpec> = {
  ETH: {
    symbol: "ETH",
    name: "Ethereum (Native)",
    address: "0x0000000000000000000000000000000000000000",
    decimals: 18,
    hotPerTxCap: "5.00000000",
    hotDailyCap: "25.00000000",
    isNative: true,
    isNonStandardErc20: false,
  },
  USDC: {
    symbol: "USDC",
    name: "USD Coin (MockUSDC)",
    address: (process.env.NEXT_PUBLIC_USDC_ADDRESS ??
      "0xe7f1725E7734CE288F8367e1Bb143E90bb3F0512") as Address,
    decimals: 6,
    hotPerTxCap: "10000.00000000",
    hotDailyCap: "50000.00000000",
    isNative: false,
    isNonStandardErc20: false,
  },
  WBTC: {
    symbol: "WBTC",
    name: "Wrapped Bitcoin (MockWBTC)",
    address: (process.env.NEXT_PUBLIC_WBTC_ADDRESS ??
      "0x9fE46736679d2D9a65F0992F2272dE9f3c7fa6e0") as Address,
    decimals: 8,
    hotPerTxCap: "0.25000000",
    hotDailyCap: "1.00000000",
    isNative: false,
    isNonStandardErc20: false,
  },
  USDT: {
    symbol: "USDT",
    name: "Tether USD (Non-Standard No-Bool)",
    address: (process.env.NEXT_PUBLIC_USDT_ADDRESS ??
      "0xCf7Ed3AccA5a467e9e704C703E8D87F634fB0Fc9") as Address,
    decimals: 6,
    hotPerTxCap: "10000.00000000",
    hotDailyCap: "50000.00000000",
    isNative: false,
    isNonStandardErc20: true,
  },
};

export const ESCROW_VAULT_ABI = [
  {
    type: "function",
    name: "depositETH",
    stateMutability: "payable",
    inputs: [],
    outputs: [],
  },
  {
    type: "function",
    name: "depositERC20",
    stateMutability: "nonpayable",
    inputs: [
      { name: "token", type: "address" },
      { name: "amount", type: "uint256" },
    ],
    outputs: [],
  },
  {
    type: "function",
    name: "withdraw",
    stateMutability: "nonpayable",
    inputs: [
      {
        name: "voucher",
        type: "tuple",
        components: [
          { name: "withdrawalId", type: "bytes32" },
          { name: "recipient", type: "address" },
          { name: "token", type: "address" },
          { name: "amount", type: "uint256" },
          { name: "nonce", type: "uint256" },
          { name: "deadline", type: "uint256" },
        ],
      },
      { name: "hotSig", type: "bytes" },
      { name: "coldSig", type: "bytes" },
    ],
    outputs: [],
  },
  {
    type: "function",
    name: "latestReserveRoot",
    stateMutability: "view",
    inputs: [],
    outputs: [{ name: "", type: "bytes32" }],
  },
  {
    type: "function",
    name: "latestReserveBlockHeight",
    stateMutability: "view",
    inputs: [],
    outputs: [{ name: "", type: "uint64" }],
  },
  {
    type: "function",
    name: "hotPerTxCap",
    stateMutability: "view",
    inputs: [{ name: "token", type: "address" }],
    outputs: [{ name: "", type: "uint256" }],
  },
  {
    type: "function",
    name: "hotDailyCap",
    stateMutability: "view",
    inputs: [{ name: "token", type: "address" }],
    outputs: [{ name: "", type: "uint256" }],
  },
  {
    type: "function",
    name: "usedNonces",
    stateMutability: "view",
    inputs: [{ name: "nonce", type: "uint256" }],
    outputs: [{ name: "", type: "bool" }],
  },
  {
    type: "event",
    name: "Deposited",
    inputs: [
      { name: "depositor", type: "address", indexed: true },
      { name: "token", type: "address", indexed: true },
      { name: "amount", type: "uint256", indexed: false },
      { name: "timestamp", type: "uint256", indexed: false },
    ],
  },
  {
    type: "event",
    name: "WithdrawalExecuted",
    inputs: [
      { name: "withdrawalId", type: "bytes32", indexed: true },
      { name: "recipient", type: "address", indexed: true },
      { name: "token", type: "address", indexed: true },
      { name: "amount", type: "uint256", indexed: false },
      { name: "nonce", type: "uint256", indexed: false },
      { name: "coldCosigned", type: "bool", indexed: false },
    ],
  },
  {
    type: "event",
    name: "ReserveRootPublished",
    inputs: [
      { name: "merkleRoot", type: "bytes32", indexed: true },
      { name: "blockHeight", type: "uint64", indexed: true },
      { name: "timestamp", type: "uint256", indexed: false },
    ],
  },
] as const;

export const ERC20_ABI = [
  {
    type: "function",
    name: "approve",
    stateMutability: "nonpayable",
    inputs: [
      { name: "spender", type: "address" },
      { name: "value", type: "uint256" },
    ],
    outputs: [{ name: "", type: "bool" }],
  },
  {
    type: "function",
    name: "allowance",
    stateMutability: "view",
    inputs: [
      { name: "owner", type: "address" },
      { name: "spender", type: "address" },
    ],
    outputs: [{ name: "", type: "uint256" }],
  },
  {
    type: "function",
    name: "balanceOf",
    stateMutability: "view",
    inputs: [{ name: "account", type: "address" }],
    outputs: [{ name: "", type: "uint256" }],
  },
  {
    type: "function",
    name: "mint",
    stateMutability: "nonpayable",
    inputs: [
      { name: "to", type: "address" },
      { name: "amount", type: "uint256" },
    ],
    outputs: [],
  },
] as const;

export const WITHDRAWAL_EIP712_DOMAIN = {
  name: "SettleCoreEscrowVault",
  version: "1",
} as const;

export const WITHDRAWAL_EIP712_TYPES = {
  Withdrawal: [
    { name: "withdrawalId", type: "bytes32" },
    { name: "recipient", type: "address" },
    { name: "token", type: "address" },
    { name: "amount", type: "uint256" },
    { name: "nonce", type: "uint256" },
    { name: "deadline", type: "uint256" },
  ],
} as const;
