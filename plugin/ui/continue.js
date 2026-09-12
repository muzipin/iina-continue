(function(){
  var heading=document.getElementById('heading'),msg=document.getElementById('message'),qr=document.getElementById('qr'),action=document.getElementById('action'),quality=document.getElementById('quality'),qualityLabel=document.getElementById('quality-label');
  var copy={
    zh:{heading:'Continue',start:'开始续播',stop:'结束续播',qrLabel:'续播二维码',quality:'播放画质',auto:'自动选择',original:'原始画质'},
    en:{heading:'Continue',start:'Start',stop:'End',qrLabel:'Continue QR code',quality:'Playback Quality',auto:'Auto',original:'Original'}
  };
  var language='zh',c=copy.zh,currentURL='',currentState={phase:'checking',active:false};
  function send(name,data){window.iina.postMessage(name,data)}
  function applyLanguage(next){language=next==='zh'?'zh':'en';c=copy[language];document.documentElement.lang=language==='zh'?'zh-CN':'en';heading.textContent=c.heading;qr.setAttribute('aria-label',c.qrLabel);qualityLabel.textContent=c.quality;quality.options[0].textContent=c.auto;quality.options[1].textContent=c.original;render(currentState)}
  function hideCode(){qr.classList.remove('revealed');qr.setAttribute('aria-pressed','false')}
  function clearCode(){currentURL='';qr.innerHTML='';qr.classList.remove('concealed');hideCode();qr.hidden=true}
  function setCode(url){if(url===currentURL)return;currentURL=url;qr.hidden=false;qr.innerHTML='';qr.classList.remove('concealed');hideCode();new QRCode(qr,{text:url,width:184,height:184,correctLevel:QRCode.CorrectLevel.M});qr.removeAttribute('title')}
  function reveal(){if(qr.hidden)return;qr.classList.remove('concealed');var next=!qr.classList.contains('revealed');qr.classList.toggle('revealed',next);qr.setAttribute('aria-pressed',String(next))}
  function render(s){var previous=currentState.phase,terminal;currentState=s||currentState;terminal=['idle','unsupported','ended','expired','error'].indexOf(currentState.phase)>=0;msg.textContent=currentState.message||'';action.textContent=currentState.active?c.stop:c.start;action.disabled=['checking','unsupported','ending'].indexOf(currentState.phase)>=0;quality.disabled=!!currentState.active||['checking','unsupported'].indexOf(currentState.phase)>=0;if(currentState.active&&!terminal&&currentState.url){setCode(currentState.url);if(previous==='waiting'&&(currentState.phase==='connected'||currentState.phase==='phone')){hideCode();qr.classList.add('concealed')}}else clearCode()}
  action.onclick=function(){send('quality',quality.value);send('toggleSession')};
  quality.onchange=function(){send('quality',quality.value)};
  qr.onclick=reveal;
  qr.onkeydown=function(e){if((e.key==='Enter'||e.key===' ')&&!qr.hidden){e.preventDefault();reveal()}else if(e.key==='Escape'){hideCode()}};
  qr.onmouseleave=hideCode;
  qr.onpointermove=function(){qr.classList.remove('concealed')};
  window.iina.onMessage('status',function(s){applyLanguage(s.language);render(s)});
  var detected=/^zh\b/i.test(navigator.language||'')?'zh':'en';applyLanguage(detected);send('language',detected);send('getState');
})();
