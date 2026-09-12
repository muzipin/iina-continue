import assert from 'node:assert/strict';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const playwrightPath = process.env.PLAYWRIGHT_PATH;
assert.ok(playwrightPath, 'PLAYWRIGHT_PATH is required');
const { chromium } = await import(pathToFileURL(`${playwrightPath}/index.mjs`));

const url=process.env.IINA_CONTINUE_TEST_URL;
assert.ok(url,'IINA_CONTINUE_TEST_URL is required');
const browser=await chromium.launch({
  headless:true,
  executablePath: process.env.CHROME_PATH,
});
for(const [scheme,locale,buttonText] of [['light','zh-CN','结束续播'],['dark','en-US','End']]){
  const page=await browser.newPage({viewport:{width:390,height:844},colorScheme:scheme,locale});
  const longTitle='A very long Continue file title that must move horizontally instead of being cut off on a narrow device screen';
  await page.route('**/state',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({state:'PREPARING',title:longTitle,durationSeconds:600,startPositionSeconds:30,lastKnownPositionSeconds:30,mediaUrl:'media',subtitleUrl:'',warning:'',mode:'direct',generation:1,revision:1,paused:true})}));
  await page.route('**/event',route=>route.fulfill({contentType:'application/json',body:'{"ok":true}'}));
  await page.goto(url,{waitUntil:'domcontentloaded'});
  await page.waitForTimeout(500);
  const result=await page.evaluate(()=>{
    const button=document.querySelector('#end'),style=getComputedStyle(button),root=document.documentElement;
    const title=document.querySelector('#title');
    return {clientHeight:root.clientHeight,scrollHeight:root.scrollHeight,visible:!!button.offsetParent,color:style.color,background:style.backgroundColor,border:style.borderColor,width:button.getBoundingClientRect().width,buttonText:button.textContent,language:root.lang,nativeControls:document.querySelector('#video').controls,buttonCount:document.querySelectorAll('button').length,titleMoves:title.classList.contains('marquee'),buttonCursor:style.cursor,videoCursor:getComputedStyle(document.querySelector('#video')).cursor,titleAttributes:document.querySelectorAll('[title]').length};
  });
  assert.equal(result.scrollHeight,result.clientHeight);
  assert.equal(result.visible,true);
  assert.ok(result.width>340);
  assert.notEqual(result.color,result.background);
  assert.equal(result.buttonText,buttonText);
  assert.equal(result.nativeControls,true);
  assert.equal(result.buttonCount,1);
  assert.equal(result.titleMoves,true);
  assert.equal(result.buttonCursor,'pointer');
  assert.equal(result.videoCursor,'pointer');
  assert.equal(result.titleAttributes,0);
  assert.equal(result.language,locale.startsWith('zh')?'zh-CN':'en');
  await page.screenshot({path:join(tmpdir(),`iina-continue-${scheme}.png`)});
  await page.click('#end');
  await page.waitForTimeout(50);
  const hint=await page.locator('#hint').textContent();
  assert.doesNotMatch(hint,/Wi-Fi|同一个 Wi-Fi/);
  await page.close();
}
await browser.close();
