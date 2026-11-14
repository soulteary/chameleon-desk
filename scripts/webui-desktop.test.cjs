const { test } = require('node:test');
const assert = require('node:assert/strict');
const fit = require('../cmd/chameleon/webui/window-geometry.js');
for (const [width,height] of [[320,480],[375,600],[768,700],[1368,520],[1920,900],[320,80]]) {
  test(`all title controls remain reachable within ${width} × ${height}`, () => {
    for (let i=0;i<8;i++) for(const [left,top] of [[40+i*25,28+i*24],[-800,-300],[width+800,height+300]]) {
      const r=fit({width,height},{width:960,height:680,left,top});
      assert.ok(r.left>=0 && r.top>=0);
      assert.ok(r.left+r.width<=width && r.top+r.height<=height);
      assert.ok(r.height>=37, 'title bar must be reachable');
      assert.deepEqual(fit({width,height},r),r,'fitting must be stable (no ResizeObserver loop)');
    }
  });
}
test('restoring a large window after viewport shrink keeps it onscreen',()=>{
  const large=fit({width:1920,height:900},{width:960,height:680,left:940,top:220});
  const small=fit({width:375,height:500},large);
  assert.ok(small.left+small.width<=375 && small.top+small.height<=500);
});
test('manual resizing cannot clip the title controls or collapse the tool content', () => {
  const small = fit({width:900,height:700},{width:1,height:1,left:10,top:10});
  assert.ok(small.width >= 560 && small.height >= 180);
  const low = fit({width:320,height:80},{width:1,height:1,left:10,top:10});
  assert.ok(low.height >= 37 && low.top + low.height <= 80);
});
