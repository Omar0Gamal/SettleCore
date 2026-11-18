import type { Metadata } from "next";
import { Inter, Plus_Jakarta_Sans } from "next/font/google";
import "./globals.css";

const inter = Inter({
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
  variable: "--font-inter",
  display: "swap",
});

const plusJakartaSans = Plus_Jakarta_Sans({
  subsets: ["latin"],
  weight: ["500", "600", "700"],
  variable: "--font-jakarta",
  display: "swap",
});

export const metadata: Metadata = {
  title: "SettleCore · Institutional Hybrid CLOB & EVM Custody Terminal",
  description:
    "Sub-microsecond C++17 CLOB matching with reorg-safe EVM EscrowVault custody and cryptographic Merkle Proof-of-Reserves.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className={`${inter.variable} ${plusJakartaSans.variable} dark`}>
      <body className="h-screen w-screen overflow-hidden bg-[#0B0E11] text-[#EAECEF] antialiased selection:bg-sky-400/25 selection:text-sky-200">
        {children}
      </body>
    </html>
  );
}
