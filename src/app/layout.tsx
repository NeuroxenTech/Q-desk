import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Q-DESK",
  description: "Quantum-Secure Digital Evidence Management",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className="dark">
      <body>{children}</body>
    </html>
  );
}
