// import './style.css';
// import './app.css';

// import Vue from 'vue';

import "bulma/css/bulma.min.css";

import { createApp } from 'vue'
import App from './App.vue'



createApp(App).mount('#app')

function adjustIframe(){
    var ifm= document.getElementById("frameA");
    ifm.height=document.documentElement.clientHeight;
    ifm.width=document.documentElement.clientWidth;
}
