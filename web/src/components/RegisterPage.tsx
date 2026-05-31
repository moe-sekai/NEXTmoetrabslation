"use client";

import React, { useState } from "react";
import { setupAdmin, setSession } from "@/lib/api";

// RegisterPage is shown on a fresh install (no users yet). It creates the first
// account, which is always an admin, then logs in with the returned token.
export function RegisterPage({ onRegister }: { onRegister: () => void }) {
  const [user, setUser] = useState("");
  const [pass, setPass] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const mismatch = confirm !== "" && pass !== confirm;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (pass !== confirm) {
      setError("两次输入的密码不一致");
      return;
    }
    setError("");
    setLoading(true);
    try {
      const res = await setupAdmin(user, pass);
      setSession(res);
      onRegister();
    } catch (err) {
      setError(err instanceof Error ? err.message : "注册失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="login-wrap">
      <form className="login-card" onSubmit={submit}>
        <h1>创建管理员</h1>
        <p className="sub">首次使用，请注册管理员账号</p>
        {error && <div className="login-error">{error}</div>}
        <input
          type="text"
          placeholder="用户名"
          value={user}
          onChange={(e) => setUser(e.target.value)}
          autoFocus
        />
        <input
          type="password"
          placeholder="密码"
          value={pass}
          onChange={(e) => setPass(e.target.value)}
        />
        <input
          type="password"
          placeholder="确认密码"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />
        {mismatch && <div className="login-error">两次输入的密码不一致</div>}
        <button
          className="btn btn-primary"
          type="submit"
          disabled={loading || !user || !pass || !confirm || mismatch}
        >
          {loading ? "创建中…" : "注册并登录"}
        </button>
      </form>
    </div>
  );
}
