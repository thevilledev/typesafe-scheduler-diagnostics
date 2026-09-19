const state = {
  items: [],
  contract: null,
  filter: "all",
  busy: false,
};

const byId = (id) => document.getElementById(id);

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function displayName(value) {
  return String(value || "unknown").replaceAll("_", " ");
}

function percent(value) {
  return `${Math.round((Number(value) || 0) * 100)}%`;
}

function setConnection(online) {
  const target = byId("connection-status");
  target.classList.toggle("online", online);
  target.classList.toggle("offline", !online);
  target.lastElementChild.textContent = online ? "Live cluster" : "Disconnected";
}

let toastTimer;
function toast(message, isError = false) {
  const target = byId("toast");
  target.textContent = message;
  target.classList.toggle("error", isError);
  target.classList.add("visible");
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => target.classList.remove("visible"), 3600);
}

function renderMetrics() {
  const items = state.items;
  const labelled = items.filter((item) => item.validation === "passed" || item.validation === "failed");
  const passed = labelled.filter((item) => item.validation === "passed").length;
  const latencies = items.map((item) => item.duration_milliseconds).filter(Number.isFinite).sort((a, b) => a - b);
  const middle = Math.floor(latencies.length / 2);
  const median = latencies.length === 0 ? null : latencies.length % 2 ? latencies[middle] : Math.round((latencies[middle - 1] + latencies[middle]) / 2);
  const tokens = items.reduce((total, item) => total + (item.result?.usage?.input_tokens || 0) + (item.result?.usage?.output_tokens || 0), 0);

  byId("metric-total").textContent = String(items.length);
  byId("metric-pass").textContent = labelled.length ? `${Math.round((passed / labelled.length) * 100)}%` : "—";
  byId("metric-latency").textContent = median === null ? "—" : `${(median / 1000).toFixed(2)}s`;
  byId("metric-tokens").textContent = tokens.toLocaleString();
}

function signal(label, value) {
  const container = element("div", "signal");
  const header = element("div", "signal-head");
  header.append(element("span", "", label), element("span", "", percent(value)));
  const meter = element("progress");
  meter.max = 1;
  meter.value = Number(value) || 0;
  container.append(header, meter);
  return container;
}

function badge(value, extraClass = "") {
  return element("span", `badge ${extraClass}`.trim(), displayName(value));
}

function decisionCard(item) {
  const card = element("article", "decision-card");
  const top = element("div", "decision-top");
  top.append(element("div", "pod-name", `${item.failure.pod.namespace}/${item.failure.pod.name}`));
  const badges = element("div", "badges");
  badges.append(badge(item.validation, item.validation));
  if (item.recommendation_published) badges.append(badge("event published", "published"));
  top.append(badges);
  card.append(top);

  if (item.error) {
    card.append(element("p", "error-copy", item.error));
  } else if (item.result) {
    const result = element("div", "decision-result");
    const remediation = element("div");
    remediation.append(element("span", "remediation-label", "Selected remediation"));
    remediation.append(element("strong", "remediation-value", displayName(item.result.remediation)));
    const confidence = element("div", "confidence");
    confidence.append(element("strong", "", percent(item.result.confidence)));
    confidence.append(element("span", "", "choice confidence"));
    result.append(remediation, confidence);
    card.append(result);

    const signals = element("div", "signal-grid");
    signals.append(
      signal("capacity", item.result.signals.capacity_shortfall),
      signal("constraints", item.result.signals.constraint_mismatch),
      signal("storage / device", item.result.signals.storage_or_device_block),
      signal("transient", item.result.signals.transient_state),
    );
    card.append(signals);
  }

  const foot = element("div", "decision-foot");
  const meta = item.result ? `${item.result.model} · ${(item.duration_milliseconds / 1000).toFixed(2)}s · ${(item.result.usage.input_tokens || 0) + (item.result.usage.output_tokens || 0)} tok` : `${(item.duration_milliseconds / 1000).toFixed(2)}s`;
  foot.append(element("span", "", meta));
  const inspect = element("button", "inspect-button", "Inspect trace →");
  inspect.type = "button";
  inspect.addEventListener("click", () => showTrace(item));
  foot.append(inspect);
  card.append(foot);
  return card;
}

function renderDecisions() {
  const list = byId("decision-list");
  list.replaceChildren();
  const items = state.filter === "all" ? state.items : state.items.filter((item) => item.validation === state.filter);
  byId("empty-state").hidden = items.length > 0;
  for (const item of items) list.append(decisionCard(item));
}

function renderContract() {
  if (!state.contract) return;
  byId("contract-model").textContent = state.contract.model;
  byId("contract-threshold").textContent = percent(state.contract.minimum_recommendation_confidence);
  const target = byId("contract-questions");
  target.replaceChildren();
  const questions = Object.entries(state.contract.questions).sort(([left], [right]) => left.localeCompare(right));
  for (const [id, question] of questions) {
    const card = element("div", "question");
    const head = element("div", "question-head");
    head.append(element("span", "question-type", question.type), element("strong", "", id));
    card.append(head, element("p", "", question.instructions));
    target.append(card);
  }
}

function traceSection(title, value, full = false) {
  const section = element("section", `trace-section${full ? " full" : ""}`);
  section.append(element("h3", "", title));
  const pre = element("pre");
  pre.textContent = JSON.stringify(value, null, 2);
  section.append(pre);
  return section;
}

function showTrace(item) {
  byId("dialog-title").textContent = `${item.failure.pod.namespace}/${item.failure.pod.name}`;
  const content = byId("dialog-content");
  content.replaceChildren();

  const verdict = {
    expected_remediation: item.expected_remediation || null,
    selected_remediation: item.result?.remediation || null,
    validation: item.validation,
    recommendation_published: item.recommendation_published,
    recommendation: item.recommendation || null,
    duration_milliseconds: item.duration_milliseconds,
    error: item.error || null,
  };
  content.append(traceSection("Code-owned validation", verdict));

  if (item.result?.probabilities) {
    const section = element("section", "trace-section");
    section.append(element("h3", "", "Choice distribution"));
    const list = element("div", "probability-list");
    const probabilities = Object.entries(item.result.probabilities).sort((left, right) => right[1] - left[1]);
    for (const [name, value] of probabilities) list.append(signal(displayName(name), value));
    section.append(list);
    content.append(section);
  }

  content.append(traceSection("Exact state sent to Jev", item.failure, true));
  content.append(traceSection("Typed result returned", item.result || {error: item.error}, true));
  byId("inspect-dialog").showModal();
}

async function refresh() {
  try {
    const response = await fetch("/api/v1/snapshot", {cache: "no-store"});
    if (!response.ok) throw new Error(`snapshot returned ${response.status}`);
    const snapshot = await response.json();
    state.items = snapshot.items || [];
    state.contract = snapshot.contract;
    setConnection(true);
    renderMetrics();
    renderDecisions();
    renderContract();
  } catch (error) {
    setConnection(false);
    console.error(error);
  }
}

function setCount(delta) {
  const input = byId("event-count");
  const value = Math.max(1, Math.min(25, Number(input.value || 1) + delta));
  input.value = String(value);
}

async function generate(event) {
  event.preventDefault();
  if (state.busy) return;
  state.busy = true;
  const button = byId("generate-button");
  button.disabled = true;
  const scenario = new FormData(event.currentTarget).get("scenario");
  const count = Number(byId("event-count").value);
  try {
    const response = await fetch("/api/v1/scenarios", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({scenario, count}),
    });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || `generation returned ${response.status}`);
    toast(`Created ${payload.created.length} real Pending Pod${payload.created.length === 1 ? "" : "s"}. Jev is evaluating them now.`);
    window.setTimeout(refresh, 500);
  } catch (error) {
    toast(error.message, true);
  } finally {
    state.busy = false;
    button.disabled = false;
  }
}

async function clearRun() {
  if (state.busy) return;
  state.busy = true;
  try {
    const response = await fetch("/api/v1/scenarios", {method: "DELETE"});
    if (!response.ok) {
      const payload = await response.json();
      throw new Error(payload.error || `cleanup returned ${response.status}`);
    }
    state.items = [];
    renderMetrics();
    renderDecisions();
    toast("Generated Pods and in-memory traces cleared.");
  } catch (error) {
    toast(error.message, true);
  } finally {
    state.busy = false;
  }
}

byId("scenario-form").addEventListener("submit", generate);
byId("count-down").addEventListener("click", () => setCount(-1));
byId("count-up").addEventListener("click", () => setCount(1));
byId("clear-button").addEventListener("click", clearRun);
byId("dialog-close").addEventListener("click", () => byId("inspect-dialog").close());
for (const button of document.querySelectorAll(".filter")) {
  button.addEventListener("click", () => {
    state.filter = button.dataset.filter;
    for (const peer of document.querySelectorAll(".filter")) peer.classList.toggle("active", peer === button);
    renderDecisions();
  });
}

refresh();
window.setInterval(refresh, 2000);
