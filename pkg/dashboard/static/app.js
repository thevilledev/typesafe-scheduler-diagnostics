const state = { items: [], contract: null, filter: "all", view: "judgment", selected: null, busy: false, connected: false };
const byId = (id) => document.getElementById(id);
const names = {
  scale_cluster: "Scale cluster",
  fix_workload_constraints: "Fix workload constraints",
  fix_storage_or_devices: "Fix storage or devices",
  inspect_scheduler_extension: "Inspect scheduler extension",
  wait: "Wait and recheck",
  unknown: "Insufficient evidence",
};
const signalNames = { capacity_shortfall: "Capacity", constraint_mismatch: "Constraints", storage_or_device_block: "Storage / devices", transient_state: "Transient state" };
const active = (item) => ["queued", "evaluating"].includes(item.stage);
const pct = (value) => `${Math.round((Number(value) || 0) * 100)}%`;
const label = (value) => names[value] || String(value || "Unknown").replaceAll("_", " ");

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function svgElement(tag, attributes = {}, text) {
  const node = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [key, value] of Object.entries(attributes)) node.setAttribute(key, value);
  if (text !== undefined) node.textContent = text;
  return node;
}

function status(message, error = false) {
  byId("run-status").textContent = message;
  byId("run-status").classList.toggle("error", error);
}

function connection(online) {
  state.connected = online;
  const target = byId("connection-status");
  target.classList.toggle("online", online);
  target.classList.toggle("offline", !online);
  target.lastElementChild.textContent = online ? "Live connection" : "Reconnecting";
  byId("generate-button").disabled = state.busy || !online;
  if (!online) {
    byId("flow-status").textContent = "Disconnected · last received state";
    byId("node-jev").classList.remove("active");
    byId("node-queue").classList.remove("active");
  }
}

// Motion represents an observed transition, never a simulated API duration.
function packet(pathID) {
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  const layer = byId("flow-packets");
  if (layer.childElementCount >= 16) return;
  const circle = svgElement("circle", { r: 3, class: "packet" });
  const motion = svgElement("animateMotion", { dur: "0.55s", repeatCount: 1, path: byId(pathID).getAttribute("d") });
  circle.append(motion);
  layer.append(circle);
  motion.beginElement();
  window.setTimeout(() => circle.remove(), 550);
}

function renderPipeline(previous) {
  const queued = state.items.filter((item) => item.stage === "queued").length;
  const evaluating = state.items.filter((item) => item.stage === "evaluating");
  const complete = state.items.filter((item) => !active(item));
  const emitted = complete.filter((item) => item.recommendation_published).length;
  byId("flow-captured").textContent = state.items.length;
  byId("flow-queued").textContent = queued;
  byId("flow-evaluating").textContent = evaluating.length;
  byId("flow-advice").textContent = emitted;
  byId("flow-review").textContent = complete.length - emitted;
  byId("node-queue").classList.toggle("active", queued > 0);
  byId("node-jev").classList.toggle("active", evaluating.length > 0);
  byId("flow-status").textContent = queued + evaluating.length > 0 ? `${queued + evaluating.length} in progress` : "Idle · listening for failures";
  byId("active-work").textContent = evaluating.length ? `Evaluating ${evaluating[0].failure.pod.name}` : queued ? `${queued} waiting for Jev` : "No active requests";
  byId("pipeline-description").textContent = `${state.items.length} captured, ${queued} queued, ${evaluating.length} evaluating, ${emitted} advice emitted, ${complete.length - emitted} held or errored. Counts cover the retained history.`;
  if (!previous) return;
  for (const item of state.items) {
    const old = previous.get(item.id);
    if (old === item.stage) continue;
    if (item.stage === "queued") packet("path-capture");
    else if (item.stage === "evaluating") packet("path-evaluate");
    else packet(item.recommendation_published ? "path-advice" : "path-review");
  }
}

function renderMetrics() {
  const complete = state.items.filter((item) => !active(item));
  const labelled = complete.filter((item) => item.expected_remediation && item.result);
  const passed = labelled.filter((item) => item.validation === "passed").length;
  const latencies = complete.filter((item) => item.result).map((item) => item.duration_milliseconds).sort((a, b) => a - b);
  const middle = Math.floor(latencies.length / 2);
  const median = latencies.length % 2 ? latencies[middle] : (latencies[middle - 1] + latencies[middle]) / 2;
  const tokens = complete.reduce((sum, item) => sum + (item.result?.usage?.input_tokens || 0) + (item.result?.usage?.output_tokens || 0), 0);
  const errors = complete.filter((item) => item.error).length;
  byId("observation-count").textContent = state.items.length;
  byId("metric-pass").textContent = labelled.length ? `${passed} / ${labelled.length}` : "—";
  byId("metric-pass-note").textContent = `${labelled.length ? "Labelled results" : "No labelled results"}${errors ? ` · ${errors} errors excluded` : ""}`;
  byId("metric-latency").textContent = latencies.length ? `${(median / 1000).toFixed(2)}s` : "—";
  byId("metric-tokens").textContent = tokens.toLocaleString();
}

function verdict(item) {
  let text = "No label", symbol = "—", className = "";
  if (active(item)) { text = item.stage === "queued" ? "Queued" : "Evaluating"; symbol = "◌"; className = "working"; }
  else if (item.error) { text = "Error"; symbol = "×"; className = "review"; }
  else if (item.validation === "passed") { text = "Match"; symbol = "✓"; }
  else if (item.validation === "failed") { text = "Mismatch"; symbol = "×"; className = "review"; }
  const node = element("span", `verdict ${className}`);
  node.append(element("span", "verdict-symbol", symbol), document.createTextNode(text));
  return node;
}

function renderRows() {
  const list = byId("decision-list");
  const focusID = document.activeElement?.closest("tr")?.dataset.id;
  const items = state.items.filter((item) => state.filter === "all" || (state.filter === "active" ? active(item) : !active(item) && (item.error || item.validation === "failed" || !item.recommendation_published)));
  list.replaceChildren();
  for (const item of items) {
    const row = element("tr", state.selected === item.id ? "selected" : "");
    row.dataset.id = item.id;
    const identity = element("td");
    const button = element("button", "pod-button", item.failure.pod.name);
    button.type = "button";
    button.title = `Inspect ${item.failure.pod.namespace}/${item.failure.pod.name}`;
    button.setAttribute("aria-label", button.title);
    identity.append(button, element("span", "row-cause", item.result ? label(item.result.remediation) : item.error ? "Diagnosis unavailable" : "Awaiting a judgment"));
    const validation = element("td");
    validation.append(verdict(item));
    row.append(identity, validation, element("td", "numeric", item.result ? pct(item.result.confidence) : "—"), element("td", "", "↗"));
    row.addEventListener("click", () => { state.selected = item.id; renderRows(); renderInspector(); });
    list.append(row);
    if (focusID === item.id) button.focus({ preventScroll: true });
  }
  const empty = byId("empty-state");
  empty.hidden = items.length > 0;
  empty.querySelector("h3").textContent = state.items.length ? "No observations in this view." : "Ready when you are.";
  empty.querySelector("p").textContent = state.items.length ? "Choose All to see the rest of the run." : "Run an experiment to see scheduler evidence become a typed judgment.";
}

function chart(entries, selected) {
  const svg = svgElement("svg", { viewBox: `0 0 360 ${entries.length * 31}`, role: "img", class: "probability-svg" });
  svg.append(svgElement("title", {}, entries.map(([name, value]) => `${name}: ${pct(value)}`).join(", ")));
  entries.forEach(([name, value], index) => {
    const y = index * 31;
    svg.append(svgElement("text", { x: 0, y: y + 12 }, name));
    svg.append(svgElement("text", { x: 360, y: y + 12, "text-anchor": "end" }, pct(value)));
    svg.append(svgElement("rect", { x: 0, y: y + 20, width: 360, height: 3, class: "bar-track" }));
    svg.append(svgElement("rect", { x: 0, y: y + 20, width: Math.max(0, Math.min(1, value)) * 360, height: 3, class: selected && name !== selected ? "bar-other" : "bar-fill" }));
  });
  return svg;
}

function jsonDetails(title, value) {
  const section = element("details");
  section.append(element("summary", "", title), element("pre", "", JSON.stringify(value, null, 2)));
  return section;
}

function meta(entries) {
  const list = element("dl", "result-meta");
  for (const [name, value] of entries) {
    const pair = element("div");
    pair.append(element("dt", "", name), element("dd", "", value));
    list.append(pair);
  }
  return list;
}

function renderInspector() {
  const container = byId("inspector-content");
  const openDetails = [...container.querySelectorAll("details[open]")].map((node) => node.querySelector("summary").textContent);
  container.replaceChildren();
  const item = state.items.find((value) => value.id === state.selected);
  byId("inspector-status").textContent = item ? (active(item) ? item.stage : "Selected observation") : "No selection";
  if (state.view === "contract") {
    if (!state.contract) { container.append(element("p", "muted", "Waiting for the decision contract.")); return; }
    container.append(meta([["Model", state.contract.model], ["Advice threshold", pct(state.contract.minimum_recommendation_confidence)]]));
    container.append(element("p", "detail-note", "These are the questions used by the scheduler. Expected validation labels are not included in model state."));
    for (const [id, question] of Object.entries(state.contract.questions)) {
      const detail = element("details");
      const summary = element("summary", "", label(id));
      summary.append(element("span", "question-type", question.type));
      detail.append(summary, element("p", "question-text", question.instructions), element("pre", "", JSON.stringify(question.criteria, null, 2)));
      container.append(detail);
    }
  } else if (!item) {
    container.append(element("p", "muted", "Select an observation to inspect its evidence and result."));
  } else {
    container.append(element("p", "inspector-pod", `${item.failure.pod.namespace} / ${item.failure.pod.name}`));
    if (state.view === "evidence") {
      container.append(element("h3", "", "What the scheduler saw"));
      container.append(element("p", "detail-note", `${item.failure.node_count} nodes evaluated · captured directly from scheduler statuses.`));
      for (const reason of item.failure.reasons || []) {
        const evidence = element("div", "evidence-reason");
        evidence.append(element("strong", "", `${reason.plugin || "Scheduler"} · ${reason.count} nodes`), element("p", "", reason.message));
        container.append(evidence);
      }
      container.append(jsonDetails("Full model state", item.failure), jsonDetails("Typed response", item.result || { stage: item.stage, error: item.error }));
    } else if (active(item)) {
      container.append(element("h3", "", item.stage === "queued" ? "Waiting for Jev" : "Evaluating the evidence"));
      container.append(element("p", "advice", item.stage === "queued" ? "The scheduler has captured the rejection evidence. This request is in the advisory queue." : "Jev is answering four independent yes/no questions and selecting a remediation."));
    } else if (item.error) {
      container.append(element("h3", "", "Diagnosis unavailable"), element("p", "advice", item.error));
    } else if (item.result) {
      const result = item.result;
      container.append(element("h3", "", label(result.remediation)));
      container.append(verdict(item));
      container.append(element("p", "advice", item.recommendation || "Advice withheld: the choice is unknown or below the confidence threshold."));
      container.append(meta([
        ["Expected label", item.expected_remediation ? label(item.expected_remediation) : "Unlabelled"],
        ["Advice gate", item.recommendation_published ? "Emitted" : "Withheld"],
        ["Choice confidence", pct(result.confidence)],
        ["Model", result.model],
      ]));
      container.append(element("p", "detail-label", "Failure signals · probability of yes"));
      container.append(chart(Object.entries(signalNames).map(([key, name]) => [name, result.signals[key]])));
      container.append(element("p", "detail-label", "Remediation distribution"));
      container.append(chart(Object.entries(result.probabilities).sort((a, b) => b[1] - a[1]).map(([name, value]) => [label(name), value]), label(result.remediation)));
      container.append(element("p", "detail-note", "Choice confidence describes distribution concentration. It does not certify that the diagnosis is correct."));
    }
  }
  for (const detail of container.querySelectorAll("details")) detail.open = openDetails.includes(detail.querySelector("summary").textContent);
}

let receivedSnapshot = false;
function applySnapshot(snapshot) {
  const previous = receivedSnapshot ? new Map(state.items.map((item) => [item.id, item.stage])) : null;
  const changed = JSON.stringify(state.items) !== JSON.stringify(snapshot.items);
  state.items = snapshot.items || [];
  state.contract = snapshot.contract;
  receivedSnapshot = true;
  if (!state.items.some((item) => item.id === state.selected)) state.selected = state.items[0]?.id || null;
  connection(true);
  renderPipeline(previous);
  if (changed || !byId("decision-list").childElementCount) {
    renderMetrics();
    renderRows();
    renderInspector();
  }
}

async function generate(event) {
  event.preventDefault();
  if (state.busy) return;
  state.busy = true;
  byId("generate-button").disabled = true;
  byId("clear-button").disabled = true;
  status("Creating scenario Pods…");
  try {
    const response = await fetch("/api/v1/scenarios", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ scenario: byId("scenario").value, count: Number(byId("event-count").value) }),
    });
    const payload = await response.json();
    if (!response.ok) throw new Error(`${payload.error} ${payload.created?.length ? `${payload.created.length} Pods were created before the error.` : ""}`);
    status(`${payload.created.length} Pods created. The pipeline updates as their diagnoses progress.`);
  } catch (error) {
    status(error.message, true);
  } finally {
    state.busy = false;
    byId("generate-button").disabled = !state.connected;
    byId("clear-button").disabled = false;
  }
}

async function clearRun() {
  if (state.busy) return;
  state.busy = true;
  byId("clear-button").disabled = true;
  byId("generate-button").disabled = true;
  try {
    const response = await fetch("/api/v1/scenarios", { method: "DELETE" });
    if (!response.ok) throw new Error((await response.json()).error);
    status("Generated Pods and traces cleared. Baseline Pods remain.");
  } catch (error) {
    status(error.message, true);
  } finally {
    state.busy = false;
    byId("clear-button").disabled = false;
    byId("generate-button").disabled = !state.connected;
  }
}

byId("scenario-form").addEventListener("submit", generate);
byId("clear-button").addEventListener("click", clearRun);
for (const button of document.querySelectorAll(".filter")) {
  button.addEventListener("click", () => {
    state.filter = button.dataset.filter;
    for (const peer of document.querySelectorAll(".filter")) {
      peer.classList.toggle("active", peer === button);
      peer.setAttribute("aria-pressed", peer === button);
    }
    renderRows();
  });
}
for (const button of document.querySelectorAll(".inspector-tab")) {
  button.addEventListener("click", () => {
    state.view = button.dataset.view;
    for (const peer of document.querySelectorAll(".inspector-tab")) {
      peer.classList.toggle("active", peer === button);
      peer.setAttribute("aria-pressed", peer === button);
    }
    renderInspector();
  });
}
byId("generate-button").disabled = true;
// Reflow the SVG itself at the app-panel breakpoint so its labels stay legible
// and both outcomes remain visible, rather than shrinking the entire diagram.
const pipelineLayout = new ResizeObserver(([entry]) => {
  const compact = entry.contentRect.width < 800;
  byId("pipeline").setAttribute("viewBox", compact ? "0 0 748 336" : "0 0 1120 236");
  byId("node-advice").setAttribute("transform", compact ? "translate(-610 206)" : "");
  byId("node-review").setAttribute("transform", compact ? "translate(-354 94)" : "");
  byId("path-advice").setAttribute("d", compact ? "M724 110H728Q738 110 738 120V202Q738 212 728 212H380Q370 212 370 222V238" : "M724 110H794Q810 110 810 94V82Q810 66 826 66H866");
  byId("path-review").setAttribute("d", compact ? "M724 110H728Q738 110 738 120V202Q738 212 728 212H636Q626 212 626 222V238" : "M724 110H794Q810 110 810 126V162Q810 178 826 178H866");
});
pipelineLayout.observe(document.querySelector(".flow-scroll"));
const events = new EventSource("/api/v1/events");
events.onmessage = (message) => {
  try { applySnapshot(JSON.parse(message.data)); }
  catch (error) { status(`Cannot display scheduler update: ${error.message}`, true); }
};
events.onerror = () => connection(false);
