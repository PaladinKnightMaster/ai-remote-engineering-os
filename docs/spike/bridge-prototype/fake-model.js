// Scripted fake OpenAI-compatible model for the bridge spike. THROWAWAY.
// Each turn: count tool results already in the conversation (k), wait DELAY s (k>0), then
// call the remote.exec tool with a scripted command; after 5 calls, call the completion tool.
const http = require('http');
const fs = require('fs');
const DELAY = Number(process.env.DELAY_SECONDS || 150) * 1000;
const LOG = process.env.MODEL_LOG || '/out/model.log';
const SCRIPT = ['echo first', 'slow approval-wait', 'echo third', 'echo fourth', 'echo fifth'];
const log = (m) => fs.appendFileSync(LOG, `${new Date().toISOString()} ${m}\n`);
let callSeq = 0;

http.createServer((req, res) => {
  let body = '';
  req.on('data', (c) => (body += c));
  req.on('end', async () => {
    if (!req.url.includes('chat/completions')) {
      log(`OTHER ${req.method} ${req.url}`);
      res.writeHead(200, { 'content-type': 'application/json' });
      return res.end(JSON.stringify({ object: 'list', data: [{ id: 'fake-model', object: 'model' }] }));
    }
    let j = {};
    try { j = JSON.parse(body); } catch {}
    const msgs = j.messages || [];
    const tools = (j.tools || []).map((t) => t.function?.name || t.name);
    const results = msgs.filter((m) => m.role === 'tool');
    const k = results.length;
    const last = results[k - 1];
    const lastText = last ? JSON.stringify(last.content).slice(0, 300) : '';
    log(`REQ stream=${!!j.stream} k=${k} tools=[${tools.join(',')}] lastResult=${lastText}`);
    const execTool = tools.find((n) => /remote/i.test(n));
    const doneTool = tools.find((n) => /complet/i.test(n));
    let call;
    if (execTool && k < SCRIPT.length) call = { name: execTool, args: { command: SCRIPT[k] } };
    else if (doneTool) call = { name: doneTool, args: { result: 'spike done' } };
    const delay = k > 0 && k <= SCRIPT.length ? DELAY : 0;
    const id = `call_${++callSeq}`;
    log(`PLAN k=${k} delay=${delay / 1000}s call=${call ? call.name + ' ' + JSON.stringify(call.args) : 'TEXT'}`);
    const base = { id: `chatcmpl-${callSeq}`, object: 'chat.completion.chunk', created: Math.floor(Date.now() / 1000), model: 'fake-model' };
    if (j.stream) {
      res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache', connection: 'keep-alive' });
      const send = (o) => res.write(`data: ${JSON.stringify(o)}\n\n`);
      send({ ...base, choices: [{ index: 0, delta: { role: 'assistant', content: '' }, finish_reason: null }] });
      const ka = setInterval(() => res.write(': keepalive\n\n'), 10000);
      await new Promise((r) => setTimeout(r, delay));
      clearInterval(ka);
      if (call) {
        send({ ...base, choices: [{ index: 0, delta: { tool_calls: [{ index: 0, id, type: 'function', function: { name: call.name, arguments: JSON.stringify(call.args) } }] }, finish_reason: null }] });
        send({ ...base, choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }] });
      } else {
        send({ ...base, choices: [{ index: 0, delta: { content: 'spike done' }, finish_reason: null }] });
        send({ ...base, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] });
      }
      send({ ...base, choices: [], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } });
      res.write('data: [DONE]\n\n');
      res.end();
    } else {
      await new Promise((r) => setTimeout(r, delay));
      const message = call
        ? { role: 'assistant', content: null, tool_calls: [{ id, type: 'function', function: { name: call.name, arguments: JSON.stringify(call.args) } }] }
        : { role: 'assistant', content: 'spike done' };
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ id: base.id, object: 'chat.completion', created: base.created, model: 'fake-model',
        choices: [{ index: 0, message, finish_reason: call ? 'tool_calls' : 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 } }));
    }
  });
}).listen(8000, () => log('fake model listening :8000'));
