"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTheme } from "next-themes";
import { useToast } from "@/app/providers";
import {
  CategoryInfo, EventStorySummary, TranslationEntry,
  clearSession, getCategories, getEntries, getEventStories, getEventStory,
  getRole, getUsername, runCNSync, triggerAITranslateAll,
  updateEntry, updateEventStoryLine, promoteEventStoryHuman, retryEventStory, reorderEventStory,
} from "@/lib/api";
import {
  CATEGORY_LABELS, FIELD_LABELS, SOURCE_LABELS, SOURCE_ORDER,
  EVENT_STORY_TITLE_MARKER, buildEventStoryEntries, eventStoryEntryLabel, parseEventStoryEntryKey,
} from "@/lib/labels";
import { useSSE } from "@/lib/sse";

interface Progress { label: string; current: number; total: number }

export function Console({ onLogout }: { onLogout: () => void }) {
  const { show } = useToast();
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  const [username] = useState(getUsername());
  const [role] = useState(getRole());

  const [categories, setCategories] = useState<CategoryInfo[]>([]);
  const [eventStories, setEventStories] = useState<EventStorySummary[]>([]);
  const [category, setCategory] = useState("");
  const [field, setField] = useState("");
  const [sourceFilter, setSourceFilter] = useState("");
  const [entries, setEntries] = useState<TranslationEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [editValue, setEditValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<Progress | null>(null);
  const editRef = useRef<HTMLTextAreaElement>(null);
  const savingRef = useRef(false);

  const isEventStory = category === "eventStory";

  useEffect(() => setMounted(true), []);

  // ---- Load categories + event stories ----
  const reloadSidebar = useCallback(() => {
    getCategories().then(setCategories).catch((e) => show(e.message, "err"));
    getEventStories().then(setEventStories).catch(() => setEventStories([]));
  }, [show]);

  useEffect(() => { reloadSidebar(); }, [reloadSidebar]);

  // ---- Load entries on selection change ----
  const loadEntries = useCallback(() => {
    if (!category || !field) return;
    setLoading(true);
    setSelectedKey(null);
    if (isEventStory) {
      getEventStory(Number(field))
        .then((detail) => {
          const list = buildEventStoryEntries(detail);
          setEntries(list);
          if (list.length) { setSelectedKey(list[0].key); setEditValue(list[0].text); }
        })
        .catch((e) => show(e.message, "err"))
        .finally(() => setLoading(false));
      return;
    }
    getEntries(category, field, sourceFilter || undefined)
      .then((data) => {
        data.sort((a, b) => {
          const d = (SOURCE_ORDER[a.source] ?? 5) - (SOURCE_ORDER[b.source] ?? 5);
          return d !== 0 ? d : a.key.localeCompare(b.key, undefined, { numeric: true });
        });
        setEntries(data);
        if (data.length) { setSelectedKey(data[0].key); setEditValue(data[0].text); }
      })
      .catch((e) => show(e.message, "err"))
      .finally(() => setLoading(false));
  }, [category, field, sourceFilter, isEventStory, show]);

  useEffect(() => { loadEntries(); }, [loadEntries]);

  // ---- SSE realtime ----
  useSSE((event, data) => {
    const d = data as Record<string, unknown>;
    if (event === "sync.progress" || event === "translate.progress") {
      setProgress({ label: String(d.detail ?? ""), current: Number(d.current ?? 0), total: Number(d.total ?? 0) });
      if (Number(d.current) >= Number(d.total)) setTimeout(() => setProgress(null), 1500);
    } else if (event === "entry.updated") {
      // Another user edited; reflect it if it's the field we're viewing.
      if (d.category === category && d.field === field && d.user !== username) {
        setEntries((prev) => prev.map((e) => (e.key === d.key ? { ...e, text: String(d.text), source: String(d.source) } : e)));
        show(`${d.user} 修改了一条翻译`, "ok");
      }
    } else if (event === "eventstory.updated") {
      if (isEventStory && Number(d.eventId) === Number(field) && d.user !== username) {
        loadEntries();
      }
    }
  }, true);

  // ---- Derived ----
  const filtered = useMemo(() => {
    if (!query) return entries;
    const q = query.toLowerCase();
    return entries.filter((e) =>
      isEventStory
        ? `${eventStoryEntryLabel(e.key)}\n${e.text}`.toLowerCase().includes(q)
        : e.key.toLowerCase().includes(q) || e.text.toLowerCase().includes(q),
    );
  }, [entries, query, isEventStory]);

  const selectedIndex = useMemo(
    () => (selectedKey ? filtered.findIndex((e) => e.key === selectedKey) : -1),
    [selectedKey, filtered],
  );
  const selectedEntry = filtered[selectedIndex] ?? null;

  useEffect(() => {
    if (selectedKey && editRef.current) {
      editRef.current.focus();
      editRef.current.select();
    }
  }, [selectedKey]);

  // ---- Actions ----
  const selectField = (cat: string, f: string) => {
    setCategory(cat); setField(f); setQuery(""); setSelectedKey(null);
  };

  const navigate = useCallback((dir: 1 | -1) => {
    if (selectedIndex < 0) return;
    const idx = selectedIndex + dir;
    if (idx < 0 || idx >= filtered.length) return;
    const next = filtered[idx];
    setSelectedKey(next.key);
    setEditValue(next.text);
    document.querySelector(`[data-key="${CSS.escape(next.key)}"]`)?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [selectedIndex, filtered]);

  const save = useCallback(async (overrideSource?: string) => {
    if (savingRef.current || !selectedKey || !category || !field) return;
    savingRef.current = true;
    const src = overrideSource || "human";
    try {
      if (isEventStory) {
        const p = parseEventStoryEntryKey(selectedKey);
        await updateEventStoryLine(Number(field), p.episodeNo, p.entryType === "title" ? "" : p.originalText, editValue, src, p.entryType);
        setEntries((prev) => prev.map((e) =>
          e.key === selectedKey
            ? { ...e, key: p.entryType === "title" ? `${p.episodeNo}|${EVENT_STORY_TITLE_MARKER}|${editValue}` : e.key, text: editValue, source: src }
            : e));
        if (p.entryType === "title") setSelectedKey(`${p.episodeNo}|${EVENT_STORY_TITLE_MARKER}|${editValue}`);
      } else {
        await updateEntry(category, field, selectedKey, editValue, src);
        setEntries((prev) => prev.map((e) => (e.key === selectedKey ? { ...e, text: editValue, source: src } : e)));
      }
      // Advance to next.
      const idx = filtered.findIndex((e) => e.key === selectedKey);
      if (idx >= 0 && idx < filtered.length - 1) {
        const next = filtered[idx + 1];
        setSelectedKey(next.key); setEditValue(next.text);
        setTimeout(() => document.querySelector(`[data-key="${CSS.escape(next.key)}"]`)?.scrollIntoView({ block: "center", behavior: "smooth" }), 40);
      } else {
        show("已到最后一条", "ok");
      }
    } catch (e) {
      show(e instanceof Error ? e.message : "保存失败", "err");
    } finally {
      savingRef.current = false;
    }
  }, [selectedKey, category, field, editValue, filtered, isEventStory, show]);

  const onTextareaKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && e.shiftKey) { e.preventDefault(); save(); }
    else if (e.key === "Escape") { setSelectedKey(null); }
    else if ((e.ctrlKey || e.metaKey) && e.key === "ArrowUp") { e.preventDefault(); navigate(-1); }
    else if ((e.ctrlKey || e.metaKey) && e.key === "ArrowDown") { e.preventDefault(); navigate(1); }
  };

  const withBusy = async (fn: () => Promise<void>) => {
    if (busy) { show("已有任务在运行", "err"); return; }
    setBusy(true);
    try { await fn(); } finally { setBusy(false); }
  };

  const doCNSync = () => withBusy(async () => {
    try { await runCNSync(); show("数据更新完成", "ok"); reloadSidebar(); loadEntries(); }
    catch (e) { show(e instanceof Error ? e.message : "更新失败", "err"); }
  });

  const doAIAll = () => withBusy(async () => {
    try {
      const r = await triggerAITranslateAll("openai") as { totalTranslated?: number; totalCandidates?: number };
      show(`AI 剧情翻译完成: ${r.totalTranslated ?? 0}/${r.totalCandidates ?? 0}`, "ok");
      reloadSidebar(); if (isEventStory) loadEntries();
    } catch (e) { show(e instanceof Error ? e.message : "AI 翻译失败", "err"); }
  });

  const currentField = categories.find((c) => c.name === category)?.fields?.find((f) => f.name === field);

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="sidebar-header">
          <h1>翻译校对</h1>
          <span className="sub">{username}{role === "admin" ? " · 管理员" : ""}</span>
        </div>

        <div className="sidebar-scroll">
          <div className="form-row" style={{ margin: "4px 6px 12px" }}>
            <label>来源过滤</label>
            <select value={sourceFilter} onChange={(e) => setSourceFilter(e.target.value)}>
              <option value="">全部</option>
              <option value="llm">仅 AI</option>
              <option value="human">仅人工</option>
              <option value="pinned">仅锁定</option>
              <option value="cn">仅官方</option>
              <option value="unknown">仅未知</option>
            </select>
          </div>

          {categories.map((cat) => (
            <div className="field-group" key={cat.name}>
              <div className="field-group-title">{CATEGORY_LABELS[cat.name] || cat.name}</div>
              {cat.fields?.map((f) => {
                const work = f.llmCount + f.unknownCount;
                const active = category === cat.name && field === f.name;
                return (
                  <div key={`${cat.name}-${f.name}`} className={`field-item ${active ? "active" : ""}`} onClick={() => selectField(cat.name, f.name)}>
                    <span>{FIELD_LABELS[f.name] || f.name}</span>
                    {work > 0 && <span className="badge work">{work}</span>}
                  </div>
                );
              })}
            </div>
          ))}

          {eventStories.length > 0 && (
            <div className="field-group">
              <div className="field-group-title">活动剧情 ({eventStories.length})</div>
              {eventStories.map((s) => {
                const active = category === "eventStory" && field === String(s.eventId);
                return (
                  <div key={s.eventId} className={`field-item ${active ? "active" : ""}`} onClick={() => selectField("eventStory", String(s.eventId))}>
                    <span>Event #{s.eventId}</span>
                    <span className="badge">{s.episodeCount}章</span>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <div className="sidebar-footer">
          <button className="btn btn-secondary" onClick={doCNSync} disabled={busy}>数据更新</button>
          <button className="btn btn-secondary" onClick={doAIAll} disabled={busy}>AI 补充剧情翻译</button>
          {role === "admin" && <a className="btn btn-ghost" href="/admin">管理设置</a>}
          {mounted && (
            <div className="form-row" style={{ margin: 0 }}>
              <label>主题</label>
              <select value={theme} onChange={(e) => setTheme(e.target.value)}>
                <option value="system">跟随系统</option>
                <option value="light">亮色</option>
                <option value="dark">深色</option>
              </select>
            </div>
          )}
          <button className="btn btn-ghost" onClick={() => { clearSession(); onLogout(); }}>退出登录</button>
        </div>
      </aside>

      <main className="main">
        {progress && (
          <div className="progress-line">
            <span>{progress.label}</span>
            <div className="progress-track">
              <div className="progress-fill" style={{ width: `${progress.total ? (progress.current / progress.total) * 100 : 0}%` }} />
            </div>
          </div>
        )}

        {!category || !field ? (
          <div className="center-state">
            <p>从左侧选择一个翻译类别</p>
          </div>
        ) : (
          <>
            <div className="main-header">
              <h2>{CATEGORY_LABELS[category] || category} / {isEventStory ? `Event #${field}` : (FIELD_LABELS[field] || field)}</h2>
              <span className="count">
                {selectedIndex >= 0 ? `${selectedIndex + 1} / ` : ""}{filtered.length} 条
                {currentField && ` （共 ${currentField.total}）`}
              </span>
            </div>

            <div className="search-bar">
              <input placeholder="搜索日文或中文…" value={query} onChange={(e) => setQuery(e.target.value)} />
            </div>

            <div className="content">
              {selectedEntry && (
                <div className="proof-panel">
                  <div className="proof-jp">
                    <span className="label">日文原文</span>
                    {selectedEntry.speakerName && <div className="speaker">{selectedEntry.speakerName}</div>}
                    <div className="jp-body">{isEventStory ? eventStoryEntryLabel(selectedEntry.key) : selectedEntry.key}</div>
                    {isEventStory && <div className="episode">第 {parseEventStoryEntryKey(selectedEntry.key).episodeNo} 章</div>}
                  </div>
                  <div className="proof-edit">
                    <div className="proof-edit-head">
                      <span className="label">翻译校对 <span className={`source-tag ${selectedEntry.source}`}>{SOURCE_LABELS[selectedEntry.source] || selectedEntry.source}</span></span>
                      <div style={{ display: "flex", gap: 6 }}>
                        <button className="btn btn-ghost btn-sm" onClick={() => navigate(-1)} disabled={selectedIndex <= 0}>↑ 上一条</button>
                        <button className="btn btn-ghost btn-sm" onClick={() => navigate(1)} disabled={selectedIndex >= filtered.length - 1}>下一条 ↓</button>
                      </div>
                    </div>
                    <textarea
                      ref={editRef}
                      className="proof-textarea"
                      value={editValue}
                      onChange={(e) => setEditValue(e.target.value)}
                      onKeyDown={onTextareaKey}
                      placeholder="输入翻译…"
                      rows={3}
                    />
                    <div className="proof-actions">
                      <button className="btn btn-primary" onClick={() => save()}>保存并下一条</button>
                      {!isEventStory && <button className="btn btn-secondary" onClick={() => save("pinned")}>锁定保存</button>}
                      {isEventStory && (
                        <>
                          <button className="btn btn-secondary" onClick={() => withBusy(async () => { await promoteEventStoryHuman(Number(field)); setEntries((p) => p.map((e) => ({ ...e, source: "human" }))); show("已整篇标记人工", "ok"); })} disabled={busy}>整篇标记人工</button>
                          <button className="btn btn-secondary" onClick={() => withBusy(async () => { await retryEventStory(Number(field)); loadEntries(); show("已重新获取剧情", "ok"); })} disabled={busy}>重新获取剧情</button>
                          <button className="btn btn-secondary" onClick={() => withBusy(async () => { await reorderEventStory(Number(field)); loadEntries(); show("已重排序对话", "ok"); })} disabled={busy}>重排序对话</button>
                        </>
                      )}
                      <div className="proof-hints">
                        <span>保存 <kbd>Shift+Enter</kbd></span>
                        <span><kbd>Ctrl+↑↓</kbd> 切换</span>
                      </div>
                    </div>
                  </div>
                </div>
              )}

              {loading ? (
                <div className="center-state"><div className="spinner" />加载中…</div>
              ) : filtered.length === 0 ? (
                <div className="center-state"><p>暂无数据</p></div>
              ) : (
                <table className="entry-table">
                  <thead>
                    <tr><th className="col-source">来源</th><th>日文原文</th><th>当前翻译</th></tr>
                  </thead>
                  <tbody>
                    {filtered.map((entry) => (
                      <tr
                        key={entry.key}
                        data-key={entry.key}
                        className={`entry-row ${selectedKey === entry.key ? "active" : ""}`}
                        onClick={() => { setSelectedKey(entry.key); setEditValue(entry.text); }}
                      >
                        <td className="col-source"><span className={`source-tag ${entry.source}`}>{SOURCE_LABELS[entry.source] || entry.source}</span></td>
                        <td>
                          <div className="jp">
                            {entry.speakerName && <div className="speaker">{entry.speakerName}</div>}
                            {isEventStory ? eventStoryEntryLabel(entry.key) : entry.key}
                          </div>
                        </td>
                        <td><div className="cn">{entry.text}</div></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </>
        )}
      </main>
    </div>
  );
}
