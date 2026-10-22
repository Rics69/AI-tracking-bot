import { HomeScreen } from "@/components/home/home-screen";

// Every screen waits for the Telegram user (Gate) and loads their data in the
// browser: there is no server-rendered UI to validate for instant navigation.
export const instant = false;

export default function Home() {
  return <HomeScreen />;
}
