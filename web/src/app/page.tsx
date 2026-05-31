"use client";

import { useEffect, useState } from "react";
import { getToken, fetchMe, clearSession, getSetupStatus } from "@/lib/api";
import { LoginPage } from "@/components/LoginPage";
import { RegisterPage } from "@/components/RegisterPage";
import { Console } from "@/components/Console";

export default function Home() {
  const [loggedIn, setLoggedIn] = useState<boolean | null>(null);
  const [needsSetup, setNeedsSetup] = useState(false);

  useEffect(() => {
    if (!getToken()) {
      // No session: ask the backend whether this is a fresh install needing
      // first-run admin registration, then show the right page.
      getSetupStatus()
        .then((s) => setNeedsSetup(s.needsSetup))
        .catch(() => setNeedsSetup(false))
        .finally(() => setLoggedIn(false));
      return;
    }
    // Validate the stored token against the backend.
    fetchMe()
      .then(() => setLoggedIn(true))
      .catch(() => {
        clearSession();
        setLoggedIn(false);
      });
  }, []);

  if (loggedIn === null) {
    return (
      <div className="center-state" style={{ height: "100vh" }}>
        <div className="spinner" />
        验证身份中…
      </div>
    );
  }
  if (!loggedIn) {
    if (needsSetup) {
      return <RegisterPage onRegister={() => { setNeedsSetup(false); setLoggedIn(true); }} />;
    }
    return <LoginPage onLogin={() => setLoggedIn(true)} />;
  }
  return <Console onLogout={() => setLoggedIn(false)} />;
}
