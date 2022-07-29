<script>
// This starter template is using Vue 3 <script setup> SFCs
// Check out https://vuejs.org/api/sfc-script-setup.html#script-setup
// import HelloWorld from './components/HelloWorld.vue'
import { GetPlgs } from '../wailsjs/go/main/App';

// let plugins = [];

// function getPlgs() {
//   console.log('getPlgs');
//   var plugins;
//   GetPlgs()
//     .then((result) => {
//       // Update result with data back from App.Greet()
//       plugins = result
//       console.log("xx", result)
//     })
//     .catch((err) => {
//       console.log("xx", err);
//       plugins = []
//     });
//   console.log("xx", plugins);
//   return plugins;
// };

export default {
  beforeCreate() {
    // plugins = getPlgs();
    // console.log("beforeCreate", plugins);
  },
  data() {
    return {
      plugins: [],
    }
  },
  created() {
    GetPlgs()
      .then((result) => {
        // Update result with data back from App.Greet()
        this.plugins = result
        if (this.plugins.length > 0) {
          this.click(this.plugins[0]);
        }
      })
      .catch((err) => {
        this.plugins = []
      });

  },
  methods: {
    click(plgName) {
      // `this` 指向该组件实例
      document.getElementById('plugin').src = `/plugin/${plgName}/index.html`;
    }
  }
}    
</script>

<template>
  <button v-if="loading" class="button is-info is-loading">Loading</button>
  <div class="tabs">
    <ul>
      <li v-for="plgName in plugins">
        <a @click="click(plgName)" :id="plgName"> {{ plgName }}</a>
      </li>
    </ul>
  </div>
  <iframe id="plugin" src="" frameborder="0" scrolling="auto" width="100%" height="100%"></iframe>

</template>

