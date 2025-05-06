import {
  isRouteErrorResponse,
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
} from "react-router";

import type { Route } from "./+types/root";
import "./app.css";

import { usePromotionStrategyStore } from '~/stores/store';
import {useEffect} from "react";

export default function App() {

  const { fetchStrategies, subscribeToStrategies, unsubscribeFromStrategies } = usePromotionStrategyStore();

  // fetchStrategies();
  // subscribeToStrategies();
  useEffect(() => {
    fetchStrategies();
    subscribeToStrategies();
    console.log("Fetching strategies...");

    return () => {
      unsubscribeFromStrategies();
      console.log("Unsubscribing from strategies...");
    };
  }, [])

  return <Outlet />;
}
