const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const YAML=require('yaml');

function readYAML(file){
 const doc=YAML.parseDocument(fs.readFileSync(file,'utf8'),{uniqueKeys:true});
 assert.deepEqual(doc.errors,[],file+' must be valid YAML');
 return doc.toJS();
}
function files(){return {workflow:readYAML('.github/workflows/check.yml'),action:readYAML('.github/actions/setup-go-cache/action.yml')};}
const runs=job=>job.steps.filter(s=>typeof s.run==='string').map(s=>s.run).join('\n');
const hasAction=(step,name)=>typeof step.uses==='string'&&step.uses.startsWith(name+'@');
function lookup(context,path){return path.split('.').reduce((node,key)=>node?.[key],context);}
// The workflow uses property lookups, an OR fallback and equality. Do not use
// eval: workflow expressions are data, and unsupported syntax must fail closed.
function expression(source,context){
 const value=source.trim();
 if(value.includes('||'))return value.split('||').map(v=>expression(v,context)).find(Boolean)||'';
 const equality=value.match(/^(.+?)\s*==\s*'([^']*)'$/);
 if(equality)return expression(equality[1],context)===equality[2];
 if(/^github(?:\.[a-zA-Z_][a-zA-Z0-9_]*)+$/.test(value))return lookup(context,value);
 throw Error('Unsupported workflow expression: '+value);
}
function evaluate(value,context){
 if(typeof value==='boolean')return value;
 const whole=String(value).match(/^\$\{\{\s*([^{}]*?)\s*\}\}$/);
 if(whole)return expression(whole[1],context);
 return String(value).replace(/\$\{\{\s*(.*?)\s*\}\}/g,(_,source)=>String(expression(source,context)??''));
}
function event(name,run,number=undefined,workflow='Checks'){
 return {github:{workflow,event_name:name,run_id:run,event:{pull_request:number===undefined?undefined:{number}}}};
}
function checkConcurrency(workflow){
 const concurrency=workflow.concurrency;assert(concurrency,'Workflow concurrency is required');
 const group=context=>evaluate(concurrency.group,context);
 const canceled=context=>evaluate(concurrency['cancel-in-progress'],context);
 assert.equal(group(event('pull_request',100,24)),group(event('pull_request',101,24)),'New runs of one PR must replace its older run');
 assert.notEqual(group(event('pull_request',100,24)),group(event('pull_request',101,25)),'Different PRs must not share a cancellation group');
 assert.notEqual(group(event('push',100)),group(event('push',101)),'Every main push must have its own group, including pending runs');
 assert.notEqual(group(event('pull_request',100,24)),group(event('push',24)),'PRs and pushes must not collide');
 assert.notEqual(group(event('pull_request',100,24)),group(event('pull_request',100,24,'Release')),'Workflows must be isolated');
 assert.equal(canceled(event('pull_request',100,24)),true);
 for(const name of ['push','workflow_dispatch','schedule'])assert.equal(canceled(event(name,100)),false,'Only PR runs may be canceled');
}
function checkTopology(workflow){
 assert.deepEqual(workflow.on.push.branches,['main']);assert(Object.hasOwn(workflow.on,'pull_request'));
 assert.deepEqual(workflow.permissions,{contents:'read'});
 const {offline,webui}=workflow.jobs;assert(offline&&webui);
 assert.deepEqual(offline.strategy.matrix.include,[{os:'ubuntu-latest',go:'1.22.x'},{os:'ubuntu-latest',go:'1.27.x'},{os:'macos-latest',go:'1.27.x'}]);
 assert.equal(offline.env.CHAMELEON_TEST_C_REQUIRED,'1');
 assert(!Object.hasOwn(webui,'needs'),'Web unit tests must run independently of Go checks');
 assert.deepEqual(webui.strategy.matrix.os,['ubuntu-latest','macos-latest'],'Preserve existing Linux and macOS Web unit coverage');
 const node=webui.steps.find(s=>hasAction(s,'actions/setup-node'));assert(node);assert.equal(node.with['node-version'],22);
 assert(!webui.steps.some(s=>hasAction(s,'actions/setup-go')||String(s.uses||'').includes('setup-go-cache')),'Web unit job must not prepare Go');
 assert.match(runs(webui),/scripts\/check\.sh webui\b/);
 assert.match(runs(webui),/npm run test:ci\b/);
 assert.match(runs(webui),/python3?.*\btest_check\.py\b/);
 assert(!offline.steps.some(s=>hasAction(s,'actions/setup-node')),'Go checks must not wait for Node setup');
 assert.doesNotMatch(runs(offline),/npm|check\.sh webui/);
 for(const stage of ['deps','go','vet','build'])assert.equal(offline.steps.filter(s=>new RegExp('scripts/check\\.sh '+stage+'\\b').test(s.run||'')).length,1,'Each Go stage must be visible as an independent CI step');
 assert(!offline.steps.some(s=>/scripts\/check\.sh\s*$/.test(s.run||'')),'CI must not accidentally run the full serial local wrapper');
 assert(offline.steps.some(s=>String(s.run||'').includes('liblzma-dev')),'Linux recovery dependencies must remain');
 assert(offline.steps.some(s=>String(s.run||'').includes('openssl@3')),'macOS recovery dependencies must remain');
 const upload=offline.steps.find(s=>hasAction(s,'actions/upload-artifact'));assert(upload,'Stage reports must be retained');assert.match(upload.if,/^(?:\$\{\{\s*)?always\(\)(?:\s*\}\})?$/);
}
function checkBridge(workflow){
 const bridge=workflow.jobs['ble-bridge'];assert(bridge);assert.deepEqual(bridge.strategy.matrix.os,['ubuntu-latest','macos-latest','windows-latest']);
 assert.equal(bridge.env.CGO_ENABLED,'0');
 const commands=runs(bridge);
 assert.match(commands,/CGO_ENABLED=1 go list/,'Keep the dependency cgo audit');
 assert.match(commands,/go test -mod=readonly \.\/\.\.\./,'Retain existing Unix Go regression coverage');
 assert.match(commands,/go build -mod=readonly -trimpath/);
 assert.match(commands,/python -m unittest discover -s tools\/ble_bridge -v/);
 assert.equal((commands.match(/python scripts\/build_distribution\.py /g)||[]).length,2,'Build both reproducibility artifacts');
 assert.match(commands,/a\.read_bytes\(\)\s*==\s*b\.read_bytes\(\)/,'Compare complete artifact bytes');
 assert.match(commands,/python scripts\/test_distribution_smoke\.py reports\/distribution/,'Keep fresh offline installation validation');
 assert(bridge.steps.some(s=>hasAction(s,'actions/upload-artifact')));
 const browser=workflow.jobs['browser-library'];assert(browser);assert.equal(browser.env.CGO_ENABLED,'0');assert.match(runs(browser),/npm run test:browser/);
}
function checkCache(workflow,action){
 assert.equal(action.runs.using,'composite');
 const steps=action.runs.steps,setup=steps.find(s=>hasAction(s,'actions/setup-go')),cache=steps.find(s=>hasAction(s,'actions/cache'));
 assert(setup&&cache);assert.equal(setup.with.cache,false,'The explicit scoped cache must not compete with setup-go cache');
 assert.match(cache.uses,/@[a-f0-9]{40}$/,'Pin the cache action revision');
 const scope=Object.keys(action.inputs).find(name=>/scope/.test(name));assert(scope,'Cache scope must be a required input');assert.equal(action.inputs[scope].required,true);
 const locate=steps.find(s=>typeof s.run==='string'&&s.run.includes('go env GOVERSION'));assert(locate,'Resolve the exact installed Go version');
 assert.match(locate.run,/go env GOCACHE/);assert.match(locate.run,/go env GOMODCACHE/);
 const key=cache.with.key;assert(key.includes('runner.os')&&key.includes('runner.arch'));assert(key.includes('inputs.'+scope));assert(key.includes('steps.'+locate.id+'.outputs.version'),'Cache key must use the exact installed toolchain version');assert(key.includes('github.sha'));assert(key.includes('go.mod')&&key.includes('go.sum'));
 const paths=String(cache.with.path).trim().split('\n');assert.equal(paths.length,2,'Cache only Go build and module directories');
 assert.deepEqual(new Set(paths),new Set(['${{ steps.'+locate.id+'.outputs.build }}','${{ steps.'+locate.id+'.outputs.modules }}']));assert(!paths.some(p=>/reports|tools\/bin|venv|node_modules/.test(p)));
 const prefixes=String(cache.with['restore-keys']).trim().split('\n');assert(prefixes.length>=1);
 for(const prefix of prefixes){assert(prefix.includes('runner.os')&&prefix.includes('runner.arch')&&prefix.includes('inputs.'+scope)&&prefix.includes('steps.'+locate.id+'.outputs.version'),'Fallback cache keys must preserve platform, toolchain and mode isolation');assert(!prefix.includes('github.sha'),'Fallback must allow incremental reuse between commits');}
 const scopes=[];for(const id of ['offline','ble-bridge','browser-library']){const use=workflow.jobs[id].steps.find(s=>s.uses==='./.github/actions/setup-go-cache');assert(use,id+' must use the scoped cache');assert(use.with[scope]);scopes.push(use.with[scope]);if(id==='offline')assert.equal(use.with['go-version'],'${{ matrix.go }}');}assert.equal(new Set(scopes).size,scopes.length,'Race/coverage, bridge and browser builds need separate cache scopes');
}

test('workflow YAML retains independent Web checks and existing platform coverage',()=>checkTopology(files().workflow));
test('only obsolete runs of the same PR share a cancellable group',()=>checkConcurrency(files().workflow));
test('bridge installation, reproducibility and browser validation remain mandatory',()=>checkBridge(files().workflow));
test('Go build cache is pinned, scoped and reusable across commits',()=>{const {workflow,action}=files();checkCache(workflow,action);});
test('policy rejects main cancellation and a serialized Web job',()=>{const {workflow}=files();const broad=structuredClone(workflow);broad.concurrency['cancel-in-progress']=true;assert.throws(()=>checkConcurrency(broad));const serial=structuredClone(workflow);serial.jobs.webui.needs='offline';assert.throws(()=>checkTopology(serial));});
test('policy rejects lost platform or reproducibility validation',()=>{const {workflow}=files();const reduced=structuredClone(workflow);reduced.jobs['ble-bridge'].strategy.matrix.os.pop();assert.throws(()=>checkBridge(reduced));const noCompare=structuredClone(workflow);for(const s of noCompare.jobs['ble-bridge'].steps)if(s.run)s.run=s.run.replace('a.read_bytes()==b.read_bytes()','True');assert.throws(()=>checkBridge(noCompare));});
test('policy rejects cross-platform cache fallbacks and artifact-directory caches',()=>{const {workflow,action}=files();const broad=structuredClone(action);broad.runs.steps.find(s=>hasAction(s,'actions/cache')).with['restore-keys']='go-cache-';assert.throws(()=>checkCache(workflow,broad));const unsafe=structuredClone(action);unsafe.runs.steps.find(s=>hasAction(s,'actions/cache')).with.path+='\nreports';assert.throws(()=>checkCache(workflow,unsafe));});
