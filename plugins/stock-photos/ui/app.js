// Stock photos page. It runs in a sandboxed iframe and only hands the run's
// inputs to the admin through the bridge; it makes no request itself.
(async () => {
  await hatamoto.ready;
  const form = document.querySelector("#form");
  const q = document.querySelector("#q");
  const count = document.querySelector("#count");
  const orientation = document.querySelector("#orientation");

  const read = () => {
    const n = Math.floor(Number(count.value));
    return {
      query: q.value.trim(),
      provider: form.elements.provider.value,
      count: Number.isFinite(n) && n >= 1 ? Math.min(30, n) : 12,
      orientation: orientation.value,
    };
  };
  const push = () => hatamoto.runInputs(read());

  form.addEventListener("input", push);
  push();
})();
