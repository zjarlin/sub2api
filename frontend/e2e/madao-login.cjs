// 真实页面回归：需要安装 playwright，并使用独立测试浏览器运行。
const {chromium}=require('playwright');
const assert=require('node:assert/strict');
const base=process.env.E2E_BASE_URL || 'http://127.0.0.1:18080';
const artifacts=process.env.E2E_ARTIFACT_DIR || '/tmp/madao-e2e';
require('node:fs').mkdirSync(artifacts,{recursive:true});
if(!process.env.E2E_EMAIL || !process.env.E2E_PASSWORD)throw Error('Set E2E_EMAIL and E2E_PASSWORD');
(async()=>{
 const browser=await chromium.launch({args:['--no-sandbox']});
 const page=await browser.newPage({viewport:{width:1500,height:1000}});
 const calls=[];
 page.on('request',r=>{if(r.url().includes('login-sessions'))calls.push({url:r.url(),body:r.postData()});});
 page.on('console',m=>{if(m.type()==='error')console.log('console',m.text())});
 page.on('requestfailed',r=>console.log('failed',r.url(),r.failure()));
 page.on('pageerror',e=>console.log('PAGEERROR',e.message));
 const response=await page.request.post(base+'/api/v1/auth/login',{data:{email:process.env.E2E_EMAIL,password:process.env.E2E_PASSWORD}});
 const login=await response.json();
 if(!response.ok())throw Error('Login failed '+response.status());
 await page.addInitScript(data=>{localStorage.setItem('admin_guide_'+data.user.id+'_admin_v4_interactive','true');localStorage.setItem('auth_token',data.access_token);localStorage.setItem('auth_user',JSON.stringify(data.user));},login.data);
 await page.goto(base+'/admin/accounts');
 await page.waitForTimeout(4000);


 await page.keyboard.press('Escape');
 await page.screenshot({path:artifacts+'/debug.png'});
 await page.getByRole('button',{name:/添加账号|Create Account|Add Account/i}).first().click();
 await page.getByTestId('platform-madao').click();
 await page.getByRole('button',{name:/登录码道|Sign in to CodeArts/}).first().click();
 await page.getByTestId('madao-login-view').waitFor({timeout:60000});
 await page.waitForTimeout(3000);
 const img=page.getByTestId('madao-login-view');

 await page.screenshot({path:artifacts+'/madao-before.png'});
 // 按截图原始视口比例点击手机号输入框。
 const point=await img.evaluate(e=>({x:695*e.clientWidth/e.naturalWidth,y:277*e.clientHeight/e.naturalHeight}));
 await img.click({position:point});
 assert.equal(await page.evaluate(()=>document.activeElement?.getAttribute('data-testid')),'builtin-login-input');
 await page.keyboard.type('13800001111',{delay:100});
 await page.waitForTimeout(6000);
 assert.equal(await page.evaluate(()=>document.activeElement?.getAttribute('data-testid')),'builtin-login-input');
 await page.keyboard.type('2');
 await page.waitForTimeout(3000);
 const texts=calls.filter(c=>c.body && JSON.parse(c.body).type==='text');
 assert.equal(texts.map(c=>JSON.parse(c.body).text).join(''),'138000011112');
 console.log('PASS keyboard typing + focus retained after screenshot refresh');
 await img.screenshot({path:artifacts+'/madao-field.png'});
 await page.screenshot({path:artifacts+'/madao-after.png'});
 // 只取消测试自身创建的会话，释放隔离浏览器。
 await page.getByTestId('builtin-adapter-login').getByRole('button',{name:/cancel|取消/i}).click();
 await browser.close();
})().catch(e=>{console.error(e);process.exit(1)});
